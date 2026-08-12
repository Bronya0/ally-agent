package service

import "testing"

// TestLooksLikeLongRunningServiceLockedPatterns 锁定长驻命令白名单行为：
// 新增模式必须命中、常见易误伤命令必须放行。防止将来误伤合法命令或漏拦长驻命令。
func TestLooksLikeLongRunningServiceLockedPatterns(t *testing.T) {
	// 命中（长驻服务/dev server）
	hit := []string{
		"python -m http.server 8000",
		"python3 -m http.server",
		"streamlit run app.py",
		"ng serve --port 4200",
		"npx react-scripts start",
		"yarn vue-cli-service serve",
		"mkdocs serve",
		"hugo server -D",
		"php artisan serve --port=8000",
		"jupyter notebook",
		"jupyter lab --no-browser",
		"nodemon src/index.js",
		// 原有模式回归
		"python manage.py runserver",
		"flask run --debug",
		"uvicorn main:app",
		"npm run dev",
	}
	for _, c := range hit {
		if !LooksLikeLongRunningService(c) {
			t.Errorf("expected blocked: %q", c)
		}
	}

	// 不命中（一次性命令 / 内置后台模式 / 不同用途）
	miss := []string{
		"gunicorn -D app:app",        // gunicorn 内置 daemon 模式，会快速返回
		"gunicorn --daemon app:app",  // 同上（长参数形式）
		"docker compose up -d",       // detached 快速返回
		"docker compose up --detach", // detached 快速返回
		"go run .",                   // 可能是 CLI
		"node server.js",             // 可能是一次性脚本
		"npm run build",              // 构建后退出
		"python script.py",           // 普通脚本
		"jupyter nbconvert --to html x.ipynb", // jupyter 子命令一次性
		"mkdocs build",               // 构建后退出
		"hugo",                       // 不带 server 的构建命令
		"ng build",                   // 构建后退出
	}
	for _, c := range miss {
		if LooksLikeLongRunningService(c) {
			t.Errorf("expected allowed: %q", c)
		}
	}
}
