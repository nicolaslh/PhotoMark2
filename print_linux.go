//go:build linux

package main

import (
	"fmt"
	"os/exec"
)

func PrintImage(path string) error {
	if lpPath, err := exec.LookPath("lp"); err == nil {
		cmd := exec.Command(lpPath, path)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("调用 lp 打印失败: %w: %s", err, string(output))
		}
		return nil
	}

	if openPath, err := exec.LookPath("xdg-open"); err == nil {
		cmd := exec.Command(openPath, path)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("打开默认图片应用失败: %w: %s", err, string(output))
		}
		return nil
	}

	return fmt.Errorf("未找到可用的 Linux 打印命令，请安装 cups/lp 或 xdg-open")
}
