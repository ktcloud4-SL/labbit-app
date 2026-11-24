package drcontrol

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func testDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, e := windows.SecurityDescriptorFromString(sddl)
	if e != nil {
		t.Fatal(e)
	}
	acl, _, e := sd.DACL()
	if e != nil {
		t.Fatal(e)
	}
	if windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil) != nil {
		t.Fatal("protect fixture DACL")
	}
}
func protectTestFile(t *testing.T, path string) {
	t.Helper()
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	testDACL(t, path, "D:P(A;;FA;;;"+u.User.Sid.String()+")")
}
func TestProtectedWindowsRejectsBroadDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if os.WriteFile(path, []byte("fixture"), 0600) != nil {
		t.Fatal("fixture")
	}
	protectTestFile(t, path)
	if _, e := ReadProtected(path); e != nil {
		t.Fatal(e)
	}
	u, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		t.Fatal(e)
	}
	testDACL(t, path, "D:P(A;;FA;;;"+u.User.Sid.String()+")(A;;FR;;;WD)")
	if _, e := ReadProtected(path); e == nil {
		t.Fatal("Everyone read accepted")
	}
}
