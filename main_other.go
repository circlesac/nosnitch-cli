//go:build !darwin

package main

func openFullDiskAccessSettings() {}

func fullDiskAccessHint() string { return "" }
