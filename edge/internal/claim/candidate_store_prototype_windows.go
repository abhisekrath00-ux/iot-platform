//go:build windows && hex_candidate_native_prototype

package claim

// Dormant native prototype. candidateOpenStore remains the fail-closed stub.
// Only explicit native acceptance tests call this implementation. Crossbuild
// proves syntax/types, never ACL, NTFS, reparse, flush or crash behavior.
import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type candidateWindowsPrototype struct {
	handle   windows.Handle
	owner    *windows.SID
	security *windows.SECURITY_DESCRIPTOR
}

func (s *candidateWindowsPrototype) Close() error { return windows.CloseHandle(s.handle) }

func candidateWindowsName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "\\/:\x00") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	upper := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	switch upper {
	case "CON", "PRN", "AUX", "NUL", "CLOCK$":
		return false
	}
	if len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9' {
		return false
	}
	return true
}
func candidateWindowsRelative(parent windows.Handle, name string, create, directory bool, sd *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	if !candidateWindowsName(name) {
		return 0, fmt.Errorf("exact native filename required")
	}
	text, e := windows.NewNTUnicodeString(name)
	if e != nil {
		return 0, e
	}
	oa := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), RootDirectory: parent, ObjectName: text, Attributes: windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE, SecurityDescriptor: sd}
	access := uint32(windows.FILE_GENERIC_READ | windows.READ_CONTROL | windows.SYNCHRONIZE)
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	disposition := uint32(windows.FILE_OPEN)
	if create {
		disposition = windows.FILE_CREATE
		access |= windows.FILE_GENERIC_WRITE
		options |= windows.FILE_WRITE_THROUGH
	}
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	e = windows.NtCreateFile(&handle, access, &oa, &status, nil, windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ, disposition, options, 0, 0)
	runtime.KeepAlive(text)
	runtime.KeepAlive(sd)
	return handle, e
}

// Strict protected DACL policy: only this user, local system and administrators
// may have allow ACEs. Unknown/object/callback/inherited ACEs refuse. This is
// intentionally narrower than ordinary NTFS defaults and never repairs ACLs.
func candidateWindowsPrivate(handle windows.Handle, owner *windows.SID) error {
	kind, e := windows.GetFileType(handle)
	if e != nil || kind != windows.FILE_TYPE_DISK {
		return fmt.Errorf("local disk object required")
	}
	var info windows.ByHandleFileInformation
	if e = windows.GetFileInformationByHandle(handle, &info); e != nil {
		return e
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("reparse point refused")
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 && (info.NumberOfLinks != 1 || info.FileSizeHigh != 0 || info.FileSizeLow > 131072) {
		return fmt.Errorf("bounded single-link file required")
	}
	sd, e := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	actual, _, e := sd.Owner()
	if e != nil || actual == nil || !actual.Equals(owner) {
		return fmt.Errorf("native object owner differs")
	}
	control, _, e := sd.Control()
	if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("protected native DACL required")
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil || acl.AceCount == 0 {
		return fmt.Errorf("nonempty protected DACL required")
	}
	for n := uint32(0); n < uint32(acl.AceCount); n++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, n, &ace); e != nil {
			return e
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&0x10 != 0 || ace.Header.AceSize < uint16(unsafe.Sizeof(windows.ACCESS_ALLOWED_ACE{})) {
			return fmt.Errorf("unsupported native ACE")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.IsValid() || (!sid.Equals(owner) && !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
			return fmt.Errorf("untrusted native allow ACE")
		}
	}
	return nil
}

func candidateOpenWindowsPrototype(dir string, create bool) (*candidateWindowsPrototype, error) {
	// Absolute drive paths only, never UNC, device namespace, relative drive,
	// alternate data stream or normalized dot/parent components.
	if !filepath.IsAbs(dir) || strings.HasPrefix(dir, "\\\\") || filepath.Clean(dir) != dir {
		return nil, fmt.Errorf("exact local absolute native path required")
	}
	volume := filepath.VolumeName(dir)
	if len(volume) != 2 || volume[1] != ':' {
		return nil, fmt.Errorf("local drive required")
	}
	parts := strings.Split(strings.TrimPrefix(dir[len(volume):], "\\"), "\\")
	if len(parts) < 2 {
		return nil, fmt.Errorf("private parent and leaf required")
	}
	for _, part := range parts {
		if !candidateWindowsName(part) {
			return nil, fmt.Errorf("unsafe native component")
		}
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, e
	}
	owner, e := user.User.Sid.Copy()
	if e != nil {
		return nil, e
	}
	security, e := windows.SecurityDescriptorFromString("O:" + owner.String() + "D:P(A;OICI;FA;;;" + owner.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if e != nil {
		return nil, e
	}
	root, e := windows.UTF16PtrFromString(volume + "\\")
	if e != nil {
		return nil, e
	}
	handle, e := windows.CreateFile(root, windows.FILE_LIST_DIRECTORY|windows.FILE_TRAVERSE|windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL|windows.SYNCHRONIZE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		return nil, e
	}
	defer func() {
		if handle != 0 {
			windows.CloseHandle(handle)
		}
	}()
	for n, name := range parts {
		leaf := n == len(parts)-1
		next, e := candidateWindowsRelative(handle, name, create && leaf, true, security)
		if e != nil {
			return nil, e
		}
		// Every intermediate handle is pinned without delete sharing. The private
		// immediate parent and attempt leaf require strict protected ACLs. Higher
		// ancestors need a separate write-authority acceptance proof before enable.
		if n >= len(parts)-2 {
			if e = candidateWindowsPrivate(next, owner); e != nil {
				windows.CloseHandle(next)
				return nil, e
			}
		}
		windows.CloseHandle(handle)
		handle = next
	}
	out := &candidateWindowsPrototype{handle: handle, owner: owner, security: security}
	handle = 0
	return out, nil
}
func (s *candidateWindowsPrototype) Read(name string) ([]byte, error) {
	h, e := candidateWindowsRelative(s.handle, name, false, false, nil)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(h), name)
	defer f.Close()
	if e = candidateWindowsPrivate(h, s.owner); e != nil {
		return nil, e
	}
	raw, e := io.ReadAll(io.LimitReader(f, 131073))
	if e != nil {
		return nil, e
	}
	if len(raw) > 131072 {
		return nil, fmt.Errorf("native read bound exceeded")
	}
	return raw, nil
}
func (s *candidateWindowsPrototype) Write(name string, raw []byte) error {
	if len(raw) > 131072 {
		return fmt.Errorf("native write bound exceeded")
	}
	h, e := candidateWindowsRelative(s.handle, name, true, false, s.security)
	if e != nil {
		return e
	}
	f := os.NewFile(uintptr(h), name)
	defer f.Close()
	if e = candidateWindowsPrivate(h, s.owner); e != nil {
		return e
	}
	n, e := f.Write(raw)
	if e != nil {
		return e
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	if e = f.Sync(); e != nil {
		return e
	}
	// Directory flush behavior must be tested natively. A failure remains a
	// failure with retained state, never permission to pretend durable success.
	return windows.FlushFileBuffers(s.handle)
}
