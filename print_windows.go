//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

func PrintImage(path string) error {
	cmd := exec.Command("rundll32.exe", "C:\\Windows\\System32\\shimgvw.dll,ImageView_PrintTo", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("调用 Windows 打印失败: %w: %s", err, string(output))
	}
	return nil
}
