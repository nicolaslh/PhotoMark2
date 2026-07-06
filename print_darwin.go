//go:build darwin

package main

import (
	"fmt"
	"os/exec"
)

func PrintImage(path string) error {
	cmd := exec.Command("open", "-a", "Preview", "-p", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("调用 macOS 打印失败: %w: %s", err, string(output))
	}
	return nil
}
