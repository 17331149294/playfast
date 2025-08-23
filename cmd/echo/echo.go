package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

var (
	port           int
	listenAddr     string
	readTimeout    time.Duration
	writeTimeout   time.Duration
	showStatistics bool
)

// 连接统计
type statistics struct {
	mu                sync.Mutex
	totalConnections  int
	activeConnections int
	totalBytes        int64
}

func (s *statistics) incrementConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalConnections++
	s.activeConnections++
}

func (s *statistics) decrementActiveConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeConnections--
}

func (s *statistics) addBytes(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalBytes += n
}

func (s *statistics) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("总连接数: %d, 活跃连接: %d, 总传输字节: %d",
		s.totalConnections, s.activeConnections, s.totalBytes)
}

func main() {
	// 解析命令行参数
	flag.IntVar(&port, "p", 19999, "服务器端口")
	flag.StringVar(&listenAddr, "a", "", "监听地址 (默认所有地址)")
	flag.DurationVar(&readTimeout, "rt", 30*time.Second, "读取超时 (例如: 30s, 1m)")
	flag.DurationVar(&writeTimeout, "wt", 30*time.Second, "写入超时 (例如: 30s, 1m)")
	flag.BoolVar(&showStatistics, "stats", true, "显示连接统计信息")
	flag.Parse()

	addr := fmt.Sprintf("%s:%d", listenAddr, port)
	log.Printf("启动 TCP echo 服务器于 %s\n", addr)

	// 创建监听器
	listen, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("无法监听: %v", err)
	}
	defer func() { _ = listen.Close() }()

	stats := &statistics{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 处理信号以优雅关闭
	go handleSignals(cancel)

	var wg sync.WaitGroup

	// 主服务循环
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				conn, err := listen.Accept()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("接受连接错误: %v", err)
					continue
				}

				stats.incrementConnections()
				// 当新连接进来时打印统计信息
				if showStatistics {
					log.Println(stats)
				}
				wg.Add(1)

				go handleConnection(ctx, conn, stats, &wg)
			}
		}
	}()

	// 等待取消信号
	<-ctx.Done()
	log.Println("正在关闭服务器...")
	_ = listen.Close() // 停止接受新连接

	// 等待所有连接处理完毕
	wg.Wait()
	log.Println("服务器已优雅关闭")
}

func handleConnection(ctx context.Context, conn net.Conn, stats *statistics, wg *sync.WaitGroup) {
	defer wg.Done()
	defer stats.decrementActiveConnections()
	defer func() { _ = conn.Close() }()

	// 设置超时
	if readTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	}
	if writeTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	}

	// 创建一个包装的writer以记录字节数
	_countingWriter := &countingWriter{writer: conn, stats: stats}

	// 使用一个done channel来确保在上下文取消时停止拷贝
	done := make(chan struct{})

	// 在goroutine中执行拷贝
	go func() {
		defer close(done)
		n, err := io.Copy(_countingWriter, conn)
		if err != nil && err != io.EOF && !isClosedConnError(err) {
			log.Printf("处理连接错误: %v", err)
		}
		log.Printf("连接关闭，共拷贝 %d 字节", n)
	}()

	// 等待复制完成或上下文取消
	select {
	case <-ctx.Done():
		_ = conn.Close() // 强制关闭连接
		<-done           // 等待拷贝goroutine退出
	case <-done:
		// 拷贝已完成
	}
}

// 记录字节数的Writer
type countingWriter struct {
	writer io.Writer
	stats  *statistics
}

func (w *countingWriter) Write(p []byte) (n int, err error) {
	n, err = w.writer.Write(p)
	if n > 0 {
		w.stats.addBytes(int64(n))
	}
	return
}

// 优雅地处理信号
func handleSignals(cancel context.CancelFunc) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	log.Printf("收到信号: %v, 开始关闭...", sig)
	cancel()
}

// 检测是否是连接关闭错误
func isClosedConnError(err error) bool {
	if err == nil {
		return false
	}

	// 检查常见的"连接关闭"错误
	if err == io.EOF {
		return true
	}

	var opErr *net.OpError
	ok := errors.As(err, &opErr)
	if ok && opErr.Err.Error() == "use of closed network connection" {
		return true
	}

	return false
}
