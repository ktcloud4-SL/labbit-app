# 1단계: 빌드 환경 (Node 22)
FROM node:22-alpine AS builder
WORKDIR /app

# 의존성 설치
COPY package*.json ./
RUN npm ci 

# 소스 복사 및 정적 파일 빌드 (dist 생성)
COPY . .
RUN npm run build

# 2단계: 실행 환경 (초경량 Nginx)
FROM nginx:alpine

# 빌드 결과물(dist)과 Nginx 설정 복사
COPY --from=builder /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
