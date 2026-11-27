package app

import (
	"path/filepath"
	"testing"
)

// 收编为后台服务的工作区目录必须比标签活得久：服务刻意脱离单轮生命周期，
// 目录删除入口要把服务计入引用。
func TestHasActiveServiceUnder(t *testing.T) {
	t.Parallel()
	a := NewApp()
	root := t.TempDir()
	sub := filepath.Join(root, "nested")
	a.services["svc-live"] = &managedService{info: ServiceInfo{ID: "svc-live", Status: "running", Cwd: sub}}
	a.services["svc-dead"] = &managedService{info: ServiceInfo{ID: "svc-dead", Status: "stopped", Cwd: root}}
	a.services["svc-elsewhere"] = &managedService{info: ServiceInfo{ID: "svc-elsewhere", Status: "running", Cwd: t.TempDir()}}

	if !a.HasActiveServiceUnder(root) {
		t.Fatal("the running service inside the root was not counted")
	}
	a.services["svc-live"].mu.Lock()
	a.services["svc-live"].info.Status = "stopped"
	a.services["svc-live"].mu.Unlock()
	if a.HasActiveServiceUnder(root) {
		t.Fatal("a stopped service still counted as holding the directory")
	}
}
