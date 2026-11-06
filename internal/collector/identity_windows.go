package collector

import "golang.org/x/sys/windows"

func publishIdentity(temporary, destination string) error {
	from, err := windows.UTF16PtrFromString(temporary)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	// Omitting REPLACE_EXISTING makes concurrent first starts retain the same completed file.
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
