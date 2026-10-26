package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/golang-module/carbon"
	"github.com/kataras/iris/v12"
	"github.com/kataras/iris/v12/middleware/recover"
	"github.com/robfig/cron/v3"
	"iptv-spider-sh/global"
	"iptv-spider-sh/initialize"
	"iptv-spider-sh/modules/auth"
	"iptv-spider-sh/modules/netbind"
	"iptv-spider-sh/router"
)

// netCheck 只做出站网卡连通性自检就退出，用于排查「认证接口 i/o timeout」这类问题。
// 必须在 initialize.Viper() 之前注册：Viper 内部会调用 flag.Parse()。
var netCheck = flag.Bool("netcheck", false, "只做出站网卡连通性自检并退出")

func main() {
	initialize.Viper()
	if *netCheck {
		runNetCheck()
		return
	}
	global.LOG = initialize.Zap()
	// 出站网卡绑定：必须在 Cache() / Gorm() / OSS() 之前初始化
	global.BIND = initialize.NetBind()
	global.CACHE = initialize.Cache()
	global.DB = initialize.Gorm()
	ossType := global.CONFIG.System.OSSType
	if ossType == "cos" {
		global.COS = initialize.COS()
	} else if ossType == "minio" {
		global.MinioClient = initialize.Minio()
	}

	initialize.InitCron()

	if global.DB != nil {
		initialize.MysqlTables(global.DB) // 初始化表
		// 程序结束前关闭数据库链接
		db, _ := global.DB.DB()
		defer db.Close()
	}

	app := iris.New()

	app.Use(recover.New())

	router.InitRouters(app)

	stb := global.CONFIG.Stb
	client, err := auth.NewGlobalClient(stb.UID, stb.SN, stb.MAC, stb.IP)
	if err != nil {
		global.LOG.Error("New AuthClient: " + err.Error())
		return
	}
	addSpiderCron(client)
	addDataCleanCron()
	addUploadCosCron()

	// 启动一个协程来运行
	go func() {
		// 启动时获取频道列表
		client.FetchChannelList()
		// 拉取一次节目单，如果近期更新过，则不会实际运行
		client.FetchChannelProg()
	}()

	app.ConfigureHost(configHost)
	_ = app.Run(iris.Addr(global.CONFIG.System.Addr), iris.WithoutServerError(iris.ErrServerClosed))

}

// 爬虫定时器
func addSpiderCron(c *auth.Client) {
	epgConfig := global.CONFIG.Epg
	var listID, progID cron.EntryID
	listID, _ = global.CRON.AddFunc("@daily", func() {
		c.FetchChannelList()
		logNexTime("Task: 获取频道信息列表;\t\tNextTime: ", listID)
	})
	logNexTime("Add Task: 获取频道信息列表;\t\tNextTime: ", listID)

	progID, _ = global.CRON.AddFunc(epgConfig.FetchCron, func() {
		c.FetchChannelProg()
		logNexTime("Task: 获取频道节目单列表;\t\tNextTime: ", progID)
	})
	logNexTime("Add Task: 获取频道节目单列表;\t\tNextTime: ", progID)
}

// 数据清理定时器
func addDataCleanCron() {
	var taskId cron.EntryID
	taskId, _ = global.CRON.AddFunc("@every 2h", func() {
		auth.CleanEPGDetailsData()
		auth.CleanChannelInfoData()
		auth.CleanChannelData()
		logNexTime("Task: 清理过期数据;\t\t\tNextTime: ", taskId)
	})
	logNexTime("Add Task: 清理过期数据;\t\t\tNextTime: ", taskId)
}

func addUploadCosCron() {
	ossConfig := global.CONFIG.OSS
	if !ossConfig.Enable || ossConfig.UploadCron == "" {
		return
	}
	var taskId0, taskId1 cron.EntryID
	taskId0, _ = global.CRON.AddFunc(ossConfig.UploadCron, func() {
		auth.GenerateAndUploadM3u()
		auth.GenerateAndUploadXmlTv()
		logNexTime("Task: 上传文件至COS;\t\tNextTime: ", taskId0)
	})
	logNexTime("Add Task: 上传文件至COS;\t\tNextTime: ", taskId0)

	taskId1, _ = global.CRON.AddFunc("0 25 0,12 * * *", func() {
		auth.GenerateAndUploadXmlTvDays7()
		logNexTime("Task: 上传至COS-7D;\t\t\tNextTime: ", taskId1)
	})
	logNexTime("Add Task: 上传至COS-7D;\t\t\tNextTime: ", taskId1)
}

func logNexTime(msg string, id cron.EntryID) {
	t := global.CRON.Entry(id).Next
	global.LOG.Info(msg + carbon.FromStdTime(t).ToString())
}

func configHost(su *iris.Supervisor) {
	su.RegisterOnShutdown(func() {
		println("Server Closed")
	})
}

// runNetCheck 打印「哪张网卡能到达 IPTV 认证服务器」的自检报告。
//
// 用法：./iptv-spider-sh -netcheck
// 它会依次用 system.dev 指定的出口、系统默认路由、以及其余每张网卡去连认证服务器，
// 直接给出「该把 system.dev 设成哪张网卡」的结论，不需要真的跑一遍认证流程。
func runNetCheck() {
	stb := global.CONFIG.Stb
	builtin := auth.Default4kLogAuthAddr
	configured := normalizeHostPort(stb.AuthHost, "7001")

	var targets []string
	switch {
	case configured == "":
		fmt.Printf("配置里没有 stb.auth_host，使用内置默认值 %s\n\n", builtin)
		targets = append(targets, builtin)
	case configured != builtin:
		// 程序内部认证目前固定用内置地址，这里一并探测，避免误判
		targets = append(targets, configured, builtin)
	default:
		targets = append(targets, configured)
	}

	netbind.Diagnose(context.Background(), global.CONFIG.System.Dev, targets, 3*time.Second, os.Stdout)
}

// normalizeHostPort 把配置里的 host / host:port / http://host 统一成 host:port。
func normalizeHostPort(v, defPort string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "http://")
	v = strings.TrimPrefix(v, "https://")
	v = strings.TrimSuffix(v, "/")
	if v == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(v); err == nil {
		return v
	}
	return net.JoinHostPort(v, defPort)
}
