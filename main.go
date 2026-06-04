package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed configs/*.json
var configFS embed.FS

//go:embed release/sing-box-android-arm64
var embeddedFS embed.FS

//go:embed web/index.html
var webFS embed.FS

// 游戏配置 - 在这里添加或移除游戏，前端会自动更新
var gameConfigs = map[string]GameInfo{
	"hp": {
		Name:     "和平精英",
		File:     "configs/config_hp.json",
		Enabled:  true,
		Icon:     "🎯",
		Platform: "Android",
	},
	"wwqy": {
		Name:     "无畏契约",
		File:     "configs/config_wwqy.json",
		Enabled:  true,
		Icon:     "🔫",
		Platform: "PC",
	},
	"sjz": {
		Name:     "三角洲行动",
		File:     "configs/config_sjz.json",
		Enabled:  true,
		Icon:     "🚁",
		Platform: "PC",
	},
	// 在这里添加新游戏示例：
	// "lol": {
	//     Name:     "英雄联盟",
	//     File:     "configs/config_lol.json",
	//     Enabled:  true,
	//     Icon:     "🏆",
	//     Platform: "PC",
	// },
	// "wzry": {
	//     Name:     "王者荣耀",
	//     File:     "configs/config_wzry.json",
	//     Enabled:  true,
	//     Icon:     "👑",
	//     Platform: "Android",
	// },
}

// 游戏信息结构
type GameInfo struct {
	Name     string `json:"name"`
	File     string `json:"file"`
	Enabled  bool   `json:"enabled"`
	Icon     string `json:"icon"`
	Platform string `json:"platform"`
}

// 持久化配置结构
type PersistentConfig struct {
	LastGame     string `json:"last_game"`
	LastUsername string `json:"last_username"`
	LastPassword string `json:"last_password"`
}

// API响应结构
type GameListResponse struct {
	Games map[string]GameInfo `json:"games"`
	Total int                 `json:"total"`
}

type SingBoxManager struct {
	cmd         *exec.Cmd
	mu          sync.RWMutex
	configPath  string
	persistPath string
}

type StartRequest struct {
	Game     string `json:"game"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type StatusResponse struct {
	Status     string `json:"status"`
	ConfigPath string `json:"configPath,omitempty"`
	Game       string `json:"game,omitempty"`
	Message    string `json:"message,omitempty"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

var manager *SingBoxManager

// 初始化管理器
func init() {
	persistPath := "/data/local/tmp/singbox-web/config.json"
	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(persistPath), 0755); err != nil {
		log.Printf("警告: 创建配置目录失败: %v", err)
	}
	manager = &SingBoxManager{
		persistPath: persistPath,
	}
}

// 获取启用的游戏列表
func getEnabledGames() map[string]GameInfo {
	enabledGames := make(map[string]GameInfo)
	for id, game := range gameConfigs {
		if game.Enabled {
			enabledGames[id] = game
		}
	}
	return enabledGames
}

// 保存配置到文件
func (m *SingBoxManager) SaveConfig(game, username, password string) error {
	config := PersistentConfig{
		LastGame:     game,
		LastUsername: username,
		LastPassword: password,
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化配置失败: %v", err)
	}

	if err := os.WriteFile(m.persistPath, data, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %v", err)
	}

	log.Printf("配置已保存到: %s (game=%s, username=%s)", m.persistPath, game, username)
	return nil
}

// 从文件加载配置
func (m *SingBoxManager) LoadConfig() (*PersistentConfig, error) {
	data, err := os.ReadFile(m.persistPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取配置文件失败: %v", err)
	}

	var config PersistentConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %v", err)
	}

	log.Printf("已加载配置: game=%s, username=%s", config.LastGame, config.LastUsername)
	return &config, nil
}

// 验证游戏是否存在且启用
func validateGame(gameID string) (GameInfo, error) {
	game, exists := gameConfigs[gameID]
	if !exists {
		return GameInfo{}, fmt.Errorf("游戏不存在: %s", gameID)
	}
	if !game.Enabled {
		return GameInfo{}, fmt.Errorf("游戏已禁用: %s", gameID)
	}
	return game, nil
}

// 统一返回JSON格式
func writeJSON(w http.ResponseWriter, data interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("JSON编码失败: %v", err)
	}
}

func writeError(w http.ResponseWriter, errMsg string, statusCode int) {
	writeJSON(w, ErrorResponse{Error: errMsg, Message: "请求失败"}, statusCode)
}

// Start 启动 sing-box
func (m *SingBoxManager) Start(configPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd != nil && m.cmd.Process != nil {
		return fmt.Errorf("sing-box 已经在运行中")
	}

	// 确保二进制文件存在
	singboxPath := "/data/local/tmp/sing-box"
	if _, err := os.Stat(singboxPath); os.IsNotExist(err) {
		singData, err := fs.ReadFile(embeddedFS, "release/sing-box-android-arm64")
		if err != nil {
			return fmt.Errorf("读取二进制文件失败: %v", err)
		}
		if err := os.WriteFile(singboxPath, singData, 0755); err != nil {
			return fmt.Errorf("写入二进制文件失败: %v", err)
		}
		log.Printf("已写入二进制文件: %s", singboxPath)
	}

	cmd := exec.Command(singboxPath, "run", "-c", configPath)
	cmd.Env = append(os.Environ(), "ENABLE_DEPRECATED_TUN_ADDRESS_X=true")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动失败: %v", err)
	}

	m.cmd = cmd
	m.configPath = configPath
	log.Printf("sing-box 已启动，配置文件: %s", configPath)

	// 监控进程退出
	go func() {
		err := cmd.Wait()
		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil {
			log.Printf("sing-box 进程退出: %v", err)
		}
		m.cmd = nil
		m.configPath = ""
	}()

	return nil
}

// Stop 停止 sing-box
func (m *SingBoxManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cmd == nil || m.cmd.Process == nil {
		return fmt.Errorf("sing-box 未运行")
	}

	if err := m.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("停止失败: %v", err)
	}

	// 清理临时配置文件
	if m.configPath != "" {
		os.Remove(m.configPath)
		log.Printf("已清理配置文件: %s", m.configPath)
	}

	m.cmd = nil
	m.configPath = ""

	return nil
}

// Status 获取运行状态
func (m *SingBoxManager) Status() StatusResponse {
	m.mu.RLock()
	defer m.mu.RUnlock()

	resp := StatusResponse{}
	if m.cmd != nil && m.cmd.Process != nil {
		resp.Status = "running"
		resp.ConfigPath = m.configPath
	} else {
		resp.Status = "stopped"
	}
	return resp
}

// buildConfig 生成配置文件
func buildConfig(gameID, username, password string) (string, error) {
	// 验证游戏
	gameInfo, err := validateGame(gameID)
	if err != nil {
		return "", err
	}

	log.Printf("使用配置模板: %s (游戏: %s)", gameInfo.File, gameInfo.Name)

	// 读取模板配置
	configBytes, err := fs.ReadFile(configFS, gameInfo.File)
	if err != nil {
		return "", fmt.Errorf("读取配置模板失败: %v", err)
	}

	// 替换占位符
	config := strings.ReplaceAll(string(configBytes), "{{USERNAME}}", username)
	config = strings.ReplaceAll(config, "{{PASSWORD}}", password)

	// 创建临时配置文件
	tmpFile, err := os.CreateTemp("", "singbox-config-*.json")
	if err != nil {
		return "", fmt.Errorf("创建临时文件失败: %v", err)
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(config); err != nil {
		return "", fmt.Errorf("写入配置失败: %v", err)
	}

	log.Printf("已生成配置文件: %s", tmpFile.Name())
	return tmpFile.Name(), nil
}

// HTTP 处理器

// 获取游戏列表
func handleGetGames(w http.ResponseWriter, r *http.Request) {
	log.Printf("获取游戏列表")
	games := getEnabledGames()
	writeJSON(w, GameListResponse{
		Games: games,
		Total: len(games),
	}, http.StatusOK)
}

// 启动代理
func handleStart(w http.ResponseWriter, r *http.Request) {
	log.Printf("收到启动请求: %s %s", r.Method, r.URL.Path)

	if r.Method != http.MethodPost {
		writeError(w, "只支持POST方法", http.StatusMethodNotAllowed)
		return
	}

	var req StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, fmt.Sprintf("无效的请求体: %v", err), http.StatusBadRequest)
		return
	}

	log.Printf("请求参数: game=%s, username=%s", req.Game, req.Username)

	if req.Game == "" || req.Username == "" || req.Password == "" {
		writeError(w, "缺少必要参数: game, username, password", http.StatusBadRequest)
		return
	}

	// 验证游戏是否存在
	if _, err := validateGame(req.Game); err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 保存配置到文件
	if err := manager.SaveConfig(req.Game, req.Username, req.Password); err != nil {
		log.Printf("保存配置失败: %v", err)
		// 不中断启动流程
	}

	// 生成配置文件
	configPath, err := buildConfig(req.Game, req.Username, req.Password)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 启动 sing-box
	if err := manager.Start(configPath); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		os.Remove(configPath)
		return
	}

	// 获取游戏名称
	gameInfo, _ := validateGame(req.Game)
	writeJSON(w, map[string]string{
		"status":    "started",
		"message":   fmt.Sprintf("%s 代理启动成功", gameInfo.Name),
		"game_name": gameInfo.Name,
	}, http.StatusOK)
}

// 停止代理
func handleStop(w http.ResponseWriter, r *http.Request) {
	log.Printf("收到停止请求: %s %s", r.Method, r.URL.Path)

	if r.Method != http.MethodPost {
		writeError(w, "只支持POST方法", http.StatusMethodNotAllowed)
		return
	}

	if err := manager.Stop(); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{
		"status":  "stopped",
		"message": "sing-box 已停止",
	}, http.StatusOK)
}

// 获取状态
func handleStatus(w http.ResponseWriter, r *http.Request) {
	status := manager.Status()
	writeJSON(w, status, http.StatusOK)
}

// 健康检查
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	}, http.StatusOK)
}

// 获取保存的配置
func handleGetConfig(w http.ResponseWriter, r *http.Request) {
	log.Printf("获取保存的配置")

	config, err := manager.LoadConfig()
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if config == nil {
		writeJSON(w, map[string]string{
			"hasConfig": "false",
		}, http.StatusOK)
		return
	}

	writeJSON(w, map[string]interface{}{
		"hasConfig": "true",
		"game":      config.LastGame,
		"username":  config.LastUsername,
		"password":  config.LastPassword,
	}, http.StatusOK)
}

// 保存配置
func handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "只支持POST方法", http.StatusMethodNotAllowed)
		return
	}

	var req StartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, fmt.Sprintf("无效的请求体: %v", err), http.StatusBadRequest)
		return
	}

	if err := manager.SaveConfig(req.Game, req.Username, req.Password); err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]string{
		"status":  "saved",
		"message": "配置已保存",
	}, http.StatusOK)
}

// 中间件：记录请求和恢复panic
func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("panic: %v", err)
				writeError(w, fmt.Sprintf("内部错误: %v", err), http.StatusInternalServerError)
			}
		}()
		next(w, r)
	}
}

func main() {
	// 支持命令行模式
	cliMode := flag.Bool("cli", false, "CLI模式（默认Web模式）")
	game := flag.String("g", "", "游戏代号")
	username := flag.String("u", "", "SOCKS5 用户名")
	password := flag.String("p", "", "SOCKS5 密码")
	port := flag.String("port", "8080", "Web服务端口")
	flag.Parse()

	// CLI模式
	if *cliMode {
		if *game == "" || *username == "" || *password == "" {
			fmt.Println("用法: ./prog -cli -g hp -u 用户名 -p 密码")
			fmt.Println("支持的游戏:")
			for id, gameInfo := range getEnabledGames() {
				fmt.Printf("  - %s: %s\n", id, gameInfo.Name)
			}
			return
		}

		configPath, err := buildConfig(*game, *username, *password)
		if err != nil {
			fmt.Println("生成配置失败:", err)
			return
		}
		defer os.Remove(configPath)

		if err := manager.Start(configPath); err != nil {
			fmt.Println("启动失败:", err)
			return
		}

		fmt.Printf("%s 已启动，按 Ctrl+C 停止\n", (*game))
		select {}
	}

	// Web模式
	// 读取HTML文件
	htmlContent, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		log.Printf("警告: 读取index.html失败: %v", err)
		log.Println("将使用默认HTML")
		htmlContent = []byte(`<html><body><h1>Sing-Box Control Panel</h1><p>API endpoints: /api/start, /api/stop, /api/status</p></body></html>`)
	}

	// 首页
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(htmlContent)
	})

	// API路由
	http.HandleFunc("/api/games", loggingMiddleware(handleGetGames))
	http.HandleFunc("/api/start", loggingMiddleware(handleStart))
	http.HandleFunc("/api/stop", loggingMiddleware(handleStop))
	http.HandleFunc("/api/status", loggingMiddleware(handleStatus))
	http.HandleFunc("/api/health", loggingMiddleware(handleHealth))
	http.HandleFunc("/api/getConfig", loggingMiddleware(handleGetConfig))
	http.HandleFunc("/api/saveConfig", loggingMiddleware(handleSaveConfig))

	addr := ":" + *port
	enabledGames := getEnabledGames()

	fmt.Printf("🚀 Web控制面板启动: http://127.0.0.1%s\n", addr)
	fmt.Printf("💾 配置文件保存路径: %s\n", manager.persistPath)
	fmt.Printf("🎮 已加载游戏数量: %d\n", len(enabledGames))
	fmt.Println("📊 API端点:")
	fmt.Printf("   - GET  %s/api/games (获取游戏列表)\n", addr)
	fmt.Printf("   - POST %s/api/start\n", addr)
	fmt.Printf("   - POST %s/api/stop\n", addr)
	fmt.Printf("   - GET  %s/api/status\n", addr)
	fmt.Printf("   - GET  %s/api/getConfig\n", addr)
	fmt.Printf("   - POST %s/api/saveConfig\n", addr)
	fmt.Println("\n游戏列表:")
	for id, game := range enabledGames {
		fmt.Printf("   %s %s (%s) - %s\n", game.Icon, game.Name, id, game.Platform)
	}
	fmt.Println("\n按 Ctrl+C 退出")

	// 启动服务器
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("HTTP服务启动失败: %v", err)
	}
}
