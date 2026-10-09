package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"yingxu/internal/app"
)

func main() {
	addr := flag.String("addr", env("VIDEO_ADDR", "127.0.0.1:8080"), "服务监听地址")
	data := flag.String("data", env("VIDEO_DATA", "data"), "工程保存目录")
	dist := flag.String("dist", "dist", "前端构建目录")
	flag.Parse()
	application, err := app.NewServer(*data, *dist)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *addr, Handler: application.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 1 << 20}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("关闭服务失败: %v", err)
		}
	}()
	log.Printf("映序后端运行于 http://%s", *addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
