//go:build !darwin && !windows

package i18n

// osLanguages 在其他系统上只看环境变量。
func osLanguages() string { return "" }
