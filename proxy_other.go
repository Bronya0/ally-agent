//go:build !windows

package main

func detectPlatformProxy() ProxyStatus { return detectEnvironmentProxy() }
