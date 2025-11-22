package keyring

import (
	"fmt"

	"golang.org/x/sys/unix"
)

type KeySerial int32

func CheckKeyringSupport() error {
	_, err := unix.KeyctlSearch(unix.KEY_SPEC_THREAD_KEYRING, "logon", "dummy", 0)
	if err == unix.ENOSYS {
		return fmt.Errorf("kernel keyring not supported")
	}
	return nil
}

func AddKeyToThreadKeyring(keyType, description string, payload []byte) (KeySerial, error) {
	keyID, err := unix.AddKey(keyType, description, payload, unix.KEY_SPEC_THREAD_KEYRING)
	if err != nil {
		return 0, fmt.Errorf("add_key syscall failed: %w", err)
	}

	return KeySerial(keyID), nil
}

func UnlinkKeyFromThreadKeyring(keyID KeySerial) error {
	_, err := unix.KeyctlInt(unix.KEYCTL_UNLINK, int(keyID), unix.KEY_SPEC_THREAD_KEYRING, 0, 0)
	if err != nil {
		return fmt.Errorf("keyctl unlink failed: %w", err)
	}

	return nil
}
