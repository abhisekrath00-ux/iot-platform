# Eastron SDM630 Modbus (three-phase energy / power meter)

Source: the manufacturer's "SDM630Modbus Smart Meter Modbus Protocol Implementation" V1.8 PDF
(eastroneurope.com/images/uploads/products/protocol/SDM630_MODBUS_Protocol.pdf), fetched 2026-10-20.
Nothing here has been run against a real meter.

## Link

2-wire RS-485, Modbus RTU, 8-bit bytes. Address 1-247, broadcast 0. Baud 2400/4800/9600/19200/38400
(factory 9600). Parity/stop factory one stop bit, no parity. Max 32 nodes per segment. Register order
factory normal = most significant register first (`abcd`); the meter can be flipped to reversed order
by writing 2141.0 to the Register Order holding register (the profile assumes the factory order).

## Measurements (read-only, input registers FC 04, float32 IEEE754, 2 registers each)

| Point | Protocol address | Documented | Unit |
|---|---|---|---|
| v1n, v2n, v3n | 0, 2, 4 | 30001, 30003, 30005 | V |
| i1, i2, i3 | 6, 8, 10 | 30007, 30009, 30011 | A |
| w1, w2, w3 | 12, 14, 16 | 30013, 30015, 30017 | W |
| va1, va2, va3 | 18, 20, 22 | 30019, 30021, 30023 | VA |
| var1, var2, var3 | 24, 26, 28 | 30025, 30027, 30029 | VAr |
| pf1, pf2, pf3 (note 1) | 30, 32, 34 | 30031, 30033, 30035 | - |
| v_avg_ln | 42 | 30043 | V |
| i_avg, i_sum | 46, 48 | 30047, 30049 | A |
| w_total | 52 | 30053 | W |
| va_total | 56 | 30057 | VA |
| var_total | 60 | 30061 | VAr |
| pf_total (note 1) | 62 | 30063 | - |
| freq | 70 | 30071 | Hz |
| kwh_import, kwh_export | 72, 74 | 30073, 30075 | kWh |
| kvarh_import, kvarh_export | 76, 78 | 30077, 30079 | kVArh |
| kvah_total | 80 | 30081 | kVAh |
| ah_total | 82 | 30083 | Ah |
| w_demand (note 2) | 84 | 30085 | W |
| v12, v23, v31 | 200, 202, 204 | 30201, 30203, 30205 | V |
| v_avg_ll | 206 | 30207 | V |
| i_neutral | 224 | 30225 | A |
| thd_v1n, thd_v2n, thd_v3n | 234, 236, 238 | 30235, 30237, 30239 | % |
| thd_i1 | 240 | 30241 | % |

Notes from the source: (1) power-factor sign shows current direction (positive = forward, negative =
reverse). (2) power demand is import minus export. Units are as printed (W/VA/VAr, not kW), scale 1.
The √/X columns in the source vary by meter variant (SDM630 Modbus V2 vs MID vs non-MID); every point
above reads on the standard SDM630Modbus. THD current rows beyond phase 1 exist (30243+) but are not
in this profile yet.

## Edge agent config

```yaml
  - id: sdm630-1
    profile: modbus-generic
    port: /dev/ttyUSB0      # COM3 on Windows
    baud: 9600
    data_bits: 8
    stop_bits: 0            # agent default = 1
    parity: none
    address: 1
    interval: 10s
    points:
      - {id: v1n, register: 0, func: 4, type: f32, word_order: abcd, unit: V, min: 0, max: 1000}
      - {id: kwh_import, register: 72, func: 4, type: f32, word_order: abcd, unit: kWh, min: 0, max: 1000000000000}
      # ... full point list in the built-in template "eastron-sdm630"
```

## Not done on purpose

Holding registers 40011-40029 (password, demand period, parity/stop, baud, pulse output, system type)
and the Register Order register are WRITABLE meter configuration; some are password-protected. The
agent only reads; configuration writes are not implemented and would need the platform's approval
path. Serial-number register 464513 (FC 03/04 per variant) is not in the profile.
