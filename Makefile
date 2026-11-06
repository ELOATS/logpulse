.PHONY: up down db backend frontend test build sample

up:            ## 一键启动全部服务（PostgreSQL + 后端 + 前端）
	docker compose up -d --build

down:
	docker compose down

db:            ## 仅启动 PostgreSQL（本地开发用）
	docker compose up -d postgres

backend:       ## 本地运行后端（需先 make db）
	cd backend && go run ./cmd/server

frontend:      ## 本地运行前端开发服务器（http://localhost:5173）
	cd frontend && npm install && npm run dev

test:          ## 单元测试；设置 LOGPULSE_TEST_DATABASE_URL 时同时运行数据库集成测试
	cd backend && go vet ./... && go test ./...
	cd frontend && npm run typecheck

build:
	cd backend && go build -o bin/server ./cmd/server
	cd frontend && npm run build

sample:        ## 生成演示日志并导入（含一次注入的故障）
	cd backend && go run ./cmd/loggen -minutes 120 > /tmp/sample.log
	curl -s -F file=@/tmp/sample.log -F service=web http://localhost:8080/api/logs/upload; echo
