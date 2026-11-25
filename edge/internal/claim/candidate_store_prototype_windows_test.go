//go:build windows && hex_candidate_native_prototype

package claim

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestCandidateWindowsPrototypeNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../key", "a\\b", "a:b", "NUL", "con.txt", "COM1", "lpt9.txt", "tail.", "tail ", "a\x00b"} {
		if candidateWindowsName(name) {
			t.Fatal("unsafe native name accepted", name)
		}
	}
	for _, name := range []string{"identity.key", "candidate-response.json", "candidate-started"} {
		if !candidateWindowsName(name) {
			t.Fatal("exact native name refused", name)
		}
	}
	// This remains true even when a native acceptance environment opts in below.
	if store, e := candidateOpenStore(`C:\private\attempt`, false); e == nil {
		store.Close()
		t.Fatal("prototype accidentally enabled product storage")
	}
}

func TestCandidateWindowsPrototypeNativeAcceptance(t *testing.T) {
	if os.Getenv("HEX_NATIVE_CANDIDATE_STORE_ACCEPTANCE") != "1" {
		t.Skip("dormant native prototype acceptance not requested")
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	security, e := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if e != nil {
		t.Fatal(e)
	}
	parent := filepath.Join(t.TempDir(), "protected-parent")
	text, e := windows.UTF16PtrFromString(parent)
	if e != nil {
		t.Fatal(e)
	}
	attrs := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: security}
	if e = windows.CreateDirectory(text, &attrs); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(parent, "attempt")
	store, e := candidateOpenWindowsPrototype(dir, true)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	// Pinning a live directory denies its rename while the prototype is open,
	// rather than following a replacement path. Test failure is not bypassed.
	if e = os.Rename(dir, dir+"-moved"); e == nil {
		t.Fatal("open native directory unexpectedly permitted replacement")
	}
	if e = store.Write("identity.key", []byte("synthetic-native-only")); e != nil {
		t.Fatal("native exclusive write/flush acceptance", e)
	}
	raw, e := store.Read("identity.key")
	if e != nil || !bytes.Equal(raw, []byte("synthetic-native-only")) {
		t.Fatal("native exact read", e)
	}
	if e = store.Write("identity.key", []byte("replace")); e == nil {
		t.Fatal("exclusive native create overwrote key")
	}
	if _, e = candidateOpenWindowsPrototype(dir, true); e == nil {
		t.Fatal("native attempt create replay allowed")
	}
	if _, e = store.Read("..\\escape"); e == nil {
		t.Fatal("native read escaped")
	}
	if e = os.Link(filepath.Join(dir, "identity.key"), filepath.Join(dir, "hardlink")); e != nil {
		t.Fatal("native hardlink test setup", e)
	}
	if _, e = store.Read("identity.key"); e == nil {
		t.Fatal("native multi-link secret accepted")
	}
	if e = os.Remove(filepath.Join(dir, "hardlink")); e != nil {
		t.Fatal(e)
	}
	publicSD, e := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;WD)")
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := publicSD.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if e = windows.SetNamedSecurityInfo(filepath.Join(dir, "identity.key"), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Read("identity.key"); e == nil {
		t.Fatal("broad native file DACL accepted")
	}
	t.Log("PASS native prototype exact protected DACL create/read/exclusive/rename/hardlink/broad-DACL refusal. This test alone does not accept ancestors, junctions, recovery/reboot or installer behavior; product storage remains disabled.")
}
