FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -tags timetzdata 将时区数据嵌入二进制，运行镜像无需安装 tzdata
RUN CGO_ENABLED=0 go build -tags timetzdata -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/loggen ./cmd/loggen

FROM alpine:3.20
RUN adduser -D -u 10001 app
COPY --from=build /out/ /usr/local/bin/
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/server"]
