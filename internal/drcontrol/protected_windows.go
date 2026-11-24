package drcontrol

import (
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

func protectedFile(f *os.File, _ string) error {
	sd, e := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return ErrDenied
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return ErrDenied
	}
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil || !owner.Equals(user.User.Sid) {
		return ErrDenied
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil {
		return ErrDenied
	}
	if acl.AceCount == 0 {
		return ErrDenied
	}
	for n := uint32(0); n < uint32(acl.AceCount); n++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(acl, n, &ace) != nil {
			return ErrDenied
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return ErrDenied
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(owner) && !sid.IsWellKnown(windows.WinLocalSystemSid) && !sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
			return ErrDenied
		}
	}
	return nil
}
