//go:build !windows && !darwin

package app

func detectPlatformProxy() ProxyStatus { return detectEnvironmentProxy() }
