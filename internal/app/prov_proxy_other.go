//go:build !windows

package app

func detectPlatformProxy() ProxyStatus { return detectEnvironmentProxy() }
