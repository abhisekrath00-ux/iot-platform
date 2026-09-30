package driver

import (
	"testing"

	"github.com/abhisekrath00-ux/iot-platform/edge/internal/config"
)

func TestOPCUAOptionsRefuseAmbiguousSecurity(t *testing.T) {
	bad := []config.Device{
		{ID: "a", Security: "sign-and-encrypt"},
		{ID: "b", Security: "weird"},
		{ID: "c", Username: "u"},
	}
	for _, d := range bad {
		if _, err := opcuaOptions(d); err == nil {
			t.Errorf("%s should be refused", d.ID)
		}
	}
	t.Setenv("OPC_PW", "x")
	if _, err := opcuaOptions(config.Device{ID: "ok", Username: "u", PasswordEnv: "OPC_PW"}); err != nil {
		t.Fatal(err)
	}
	if _, err := opcuaOptions(config.Device{ID: "ok2", Security: "sign", ClientCert: "c.pem", ClientKey: "k.pem"}); err != nil {
		t.Fatal(err)
	}
}
