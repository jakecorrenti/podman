//go:build windows
// +build windows

package machine

import (
	"os"
	"syscall"
	"time"
)

func GetProcessState(pid int) (active bool, exitCode int) {
	const da = syscall.STANDARD_RIGHTS_READ | syscall.PROCESS_QUERY_INFORMATION | syscall.SYNCHRONIZE
	handle, err := syscall.OpenProcess(da, false, uint32(pid))
	if err != nil {
		return false, int(syscall.ERROR_PROC_NOT_FOUND)
	}

	var code uint32
	syscall.GetExitCodeProcess(handle, &code)
    // 259 means still active. The return value gives this number context, but
    // I think that it would be worthwhile to make it a constant so its easier to
    // read and doesn't require me to look at the return value to know what it
    // means
	return code == 259, int(code)
}

func PipeNameAvailable(pipeName string) bool {
	_, err := os.Stat(`\\.\pipe\` + pipeName)
	return os.IsNotExist(err)
}

func WaitPipeExists(pipeName string, retries int, checkFailure func() error) error {
	var err error
	for i := 0; i < retries; i++ {
		_, err = os.Stat(`\\.\pipe\` + pipeName)
		if err == nil {
			break
		}
		if fail := checkFailure(); fail != nil {
			return fail
		}
		time.Sleep(250 * time.Millisecond)
	}

	return err
}
