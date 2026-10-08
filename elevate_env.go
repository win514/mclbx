package main

// elevate_env.go 读取提权开关：只依赖环境变量，与平台无关。

import (
	"os"
	"strings"
)

// envNoElevate 读 MCLBX_NO_ELEVATE，为 1/true/yes 时全程不提权。
func envNoElevate() bool {
	v := strings.TrimSpace(os.Getenv("MCLBX_NO_ELEVATE"))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}
