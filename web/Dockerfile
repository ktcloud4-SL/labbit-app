# 1단계: 빌드 환경 (Node 22)
FROM node:22-alpine AS builder
WORKDIR /app

# 의존성 설치
COPY package*.json ./
RUN npm ci 

# 소스 복사 및 정적 파일 빌드 (dist 생성)
COPY . .

# PUBLIC Vite config is baked into dist at IMAGE BUILD TIME. The default is
# intentionally empty until LBT-181 approves the Installer HTTPS URL.
# Do not pass enrollment tokens, credentials or other secrets as build args.
ARG VITE_LABBIT_APPROVED_INSTALLER_URL=""
ENV VITE_LABBIT_APPROVED_INSTALLER_URL=${VITE_LABBIT_APPROVED_INSTALLER_URL}
RUN node scripts/validate-installer-build-env.mjs && npm run build

# 2단계: 비루트 Nginx 실행 환경
FROM nginxinc/nginx-unprivileged:stable-alpine

# 빌드 결과물(dist)과 Nginx 설정 복사
COPY --chown=101:101 --from=builder /app/dist /usr/share/nginx/html
COPY --chown=101:101 nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 8080

USER 101:101
CMD ["nginx", "-g", "daemon off;"]
