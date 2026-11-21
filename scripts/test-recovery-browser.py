#!/usr/bin/env python3
"""Opt-in real React/browser -> HTTP handlers -> disposable local PostgreSQL.

Requires node, installed web dependencies, Chrome, websocket-client, Go and local
PG with CREATE DATABASE. No installed account profile is used. No CA/broker/device
runs. Normal CI skips this test. Screenshots contain synthetic fixture data only.
"""
import argparse
import base64
import json
import os
import pathlib
import shutil
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def child(config_path):
    import websocket
    config = json.loads(pathlib.Path(config_path).read_text())
    root = pathlib.Path(config['root'])
    web = root / 'web'
    chrome = os.environ.get('TEST_RECOVERY_CHROME') or shutil.which('google-chrome') or shutil.which('chromium')
    if not chrome:
        raise RuntimeError('Chrome is required')
    processes = []
    with tempfile.TemporaryDirectory(prefix='.recovery-browser-', dir=web) as fixture, tempfile.TemporaryDirectory() as private:
        folder = pathlib.Path(fixture)
        private = pathlib.Path(private)
        vite_port, chrome_port = port(), port()
        (folder / 'index.html').write_text('<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1.0"><title>Isolated recovery acceptance</title></head><body><div id="root"></div><script type="module" src="/main.tsx"></script></body></html>')
        (folder / 'main.tsx').write_text('''import React from 'react';
import {createRoot} from 'react-dom/client';
import SetupRecovery from '../src/components/SetupRecovery';
import SecurePairing from '../src/components/SecurePairing';
import '../src/styles.css';
const pairing=new URLSearchParams(location.search).has('pairing');
createRoot(document.getElementById('root')!).render(<main><h1>Isolated recovery acceptance</h1><p>Synthetic gateway and disposable database. No broker or device.</p>{pairing?<SecurePairing/>:<SetupRecovery/>}</main>);
''')
        # Explicit object construction keeps executable source separate from data.
        (folder / 'vite.config.mjs').write_text('import {defineConfig} from "vite";import react from "@vitejs/plugin-react";const config='+json.dumps({'root':str(folder),'server':{'host':'127.0.0.1','port':vite_port,'strictPort':True,'proxy':{'/v1':{'target':config['endpoint']}}}})+';config.plugins=[react()];export default defineConfig(config);')
        try:
            log = open(private / 'process.log', 'w')
            processes.append(subprocess.Popen(['node',str(web/'node_modules/vite/bin/vite.js'),'--config',str(folder/'vite.config.mjs')],cwd=web,stdout=log,stderr=subprocess.STDOUT))
            processes.append(subprocess.Popen([chrome,'--headless=new','--no-sandbox','--disable-gpu','--remote-allow-origins=*','--remote-debugging-port='+str(chrome_port),'--user-data-dir='+str(private/'chrome'),'about:blank'],stdout=log,stderr=subprocess.STDOUT))
            origin='http://127.0.0.1:'+str(vite_port)
            for _ in range(150):
                try:
                    urllib.request.urlopen(origin,timeout=.3).close()
                    tabs=json.load(urllib.request.urlopen('http://127.0.0.1:'+str(chrome_port)+'/json',timeout=.3))
                    break
                except Exception:
                    time.sleep(.1)
            else:
                raise RuntimeError('isolated browser/Vite startup failed')
            ws=websocket.create_connection(tabs[0]['webSocketDebuggerUrl'],timeout=10)
            seq=0
            def command(method,params=None):
                nonlocal seq
                seq+=1
                ws.send(json.dumps({'id':seq,'method':method,'params':params or {}}))
                while True:
                    data=json.loads(ws.recv())
                    if data.get('id')==seq:
                        if 'error' in data:
                            raise RuntimeError('browser command failed')
                        return data.get('result',{})
            def js(expression):
                result=command('Runtime.evaluate',{'expression':expression,'returnByValue':True,'awaitPromise':True})
                if 'exceptionDetails' in result:
                    raise RuntimeError('browser evaluation failed')
                return result.get('result',{}).get('value')
            def wait(expression):
                for _ in range(150):
                    try:
                        if js(expression):
                            return
                    except RuntimeError:
                        # Navigation can briefly have no body/execution context.
                        # Only the read condition is repeated, never a click or POST.
                        pass
                    time.sleep(.05)
                print('Unmet condition, rendered page:',js('document.body.innerText.slice(0,1200)'))
                print('Read diagnostics:',js('fetch("/v1/commissioning/sessions",{headers:{Authorization:"Bearer "+localStorage.getItem("iot.token")}}).then(async r=>({status:r.status,body:(await r.text()).slice(0,300)})).catch(()=>({network_failed:true}))'))
                if config['artifacts']:
                    out=pathlib.Path(config['artifacts']);out.mkdir(parents=True,exist_ok=True)
                    (out/'unaccepted-failure.png').write_bytes(base64.b64decode(command('Page.captureScreenshot',{'format':'png','captureBeyondViewport':True})['data']))
                raise RuntimeError('browser acceptance condition not met')
            def click(text):
                js('Array.from(document.querySelectorAll("button")).find(b=>b.textContent==='+json.dumps(text)+').click()')
            def screenshot(name,width,height):
                command('Emulation.setDeviceMetricsOverride',{'width':width,'height':height,'deviceScaleFactor':1,'mobile':width<500})
                time.sleep(.1)
                if js('document.documentElement.scrollWidth>window.innerWidth'):
                    raise RuntimeError('horizontal overflow')
                if config['artifacts']:
                    out=pathlib.Path(config['artifacts']);out.mkdir(parents=True,exist_ok=True)
                    (out/(name+'.png')).write_bytes(base64.b64decode(command('Page.captureScreenshot',{'format':'png','captureBeyondViewport':True})['data']))
            command('Page.enable')
            command('Page.addScriptToEvaluateOnNewDocument',{'source':'localStorage.setItem("iot.token",'+json.dumps(config['token'])+');localStorage.setItem("hexmon-tour-done","1");'})
            command('Page.navigate',{'url':origin})
            wait('document.body.innerText.includes("Add another source")')
            click('Add another source')
            wait('document.body.innerText.includes("Source setup outcome unknown")')
            if not js('Array.from(document.querySelectorAll("button")).find(b=>b.textContent==="Source setup outcome unknown").disabled'):
                raise RuntimeError('unknown source action rearmed')
            click('Reload saved setups')
            wait('document.querySelectorAll("a").length===2&&document.body.innerText.includes("No source assigned")')
            recovered=js('Array.from(document.querySelectorAll("a")).map(a=>a.getAttribute("href")).find(h=>h!=="/onboarding?session=configured-session")')
            if not recovered or not recovered.startswith('/onboarding?session='):
                raise RuntimeError('committed source session not observed')
            screenshot('source-committed-lost-response-desktop',1200,1100)
            screenshot('source-committed-lost-response-mobile',390,1400)
            command('Page.navigate',{'url':origin+'?pairing=1'})
            wait('document.body.innerText.includes("Load pairing requests")')
            click('Load pairing requests')
            wait('document.body.innerText.includes("Status: pending")')
            click('Revoke pending request')
            wait('document.body.innerText.includes("Decision response unknown")')
            if not js('Array.from(document.querySelectorAll("article button")).every(b=>b.disabled)'):
                raise RuntimeError('unknown pairing decision rearmed')
            screenshot('pairing-revoke-unknown-mobile',390,1700)
            click('Reload pairing requests')
            wait('document.body.innerText.includes("Status: revoked")')
            if not js('document.querySelector("article input")===null&&document.body.innerText.includes("not a confirmed response to that decision")'):
                raise RuntimeError('observed revocation lost uncertainty distinction')
            screenshot('pairing-revoke-observed-desktop',1200,1200)
            screenshot('pairing-revoke-observed-mobile',390,1600)
            ws.close()
            print(json.dumps({'recovered_href':recovered,'pairing_observed':'revoked','unknown_response_retained':True}))
        finally:
            for process in reversed(processes):
                process.terminate()
                try:
                    process.wait(timeout=4)
                except subprocess.TimeoutExpired:
                    process.kill();process.wait()
            if 'log' in locals():
                log.close()


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--browser-child',type=pathlib.Path)
    parser.add_argument('--artifacts',type=pathlib.Path)
    args=parser.parse_args()
    if args.browser_child:
        child(args.browser_child)
        return 0
    root=pathlib.Path(__file__).resolve().parents[1]
    env=dict(os.environ,TEST_RECOVERY_BROWSER='1',TEST_RECOVERY_PYTHON=sys.executable,TEST_UI_ARTIFACT_DIR=str(args.artifacts.resolve()) if args.artifacts else '')
    return subprocess.call(['go','test','-race','./cmd/api','-run','^TestIntegrationBrowserLostSetupAndPairingResponses$','-count=1','-v'],cwd=root/'server',env=env)


if __name__=='__main__':
    sys.exit(main())
