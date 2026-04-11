.PHONY: dev db db-stop migrate resume-agent frontend

# 启动基础设施（PostgreSQL + MySQL + Redis）
db:
	docker-compose up -d

# 停止基础设施
db-stop:
	docker-compose down

# 执行数据库迁移
migrate:
	cd services/resume-agent && go run cmd/migrate/main.go

# 启动 Resume Agent
resume-agent:
	cd services/resume-agent && go run cmd/server/main.go

# 启动前端
frontend:
	cd frontend && npm run dev

# 同时启动（需要先启动db）
dev: db
	echo "Starting resume-agent and frontend..."
	make resume-agent & make frontend