.PHONY: dev db db-stop migrate seed build

# 启动PostgreSQL
db:
	docker-compose up -d

# 停止PostgreSQL
db-stop:
	docker-compose down

# 执行数据库迁移
migrate:
	cd backend && go run cmd/migrate/main.go

# 启动后端
backend:
	cd backend && go run cmd/server/main.go

# 启动前端
frontend:
	cd frontend && npm run dev

# 同时启动前后端（需要先启动db）
dev: db
	echo "Starting backend and frontend..."
	make backend & make frontend