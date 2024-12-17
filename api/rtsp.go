package api

import (
	"bufio"
	"ginrtsp/service"

	"log"
	"os/exec"

	"github.com/gin-gonic/gin"
)

// PlayRTSP 启动 FFMPEG 播放 RTSP 流
func PlayRTSP(c *gin.Context) {
	srv := &service.RTSPTransSrv{}
	if err := c.ShouldBind(srv); err != nil {
		c.JSON(400, errorRequest(err))
		return
	}

	ret := srv.Service()
	c.JSON(ret.Code, ret)
}

func SaveRecord(c *gin.Context) {
	srv := &service.RTSPSaveSrv{}
	if err := c.ShouldBind(srv); err != nil {
		c.JSON(400, errorRequest(err))
		return
	}

	ret := srv.SaveService()
	c.JSON(ret.Code, ret)
}

func StopSave(c *gin.Context) {
	srv := &service.RTSPStopSrv{}
	if err := c.ShouldBind(srv); err != nil {
		c.JSON(400, errorRequest(err))
		return
	}

	ret := srv.StopService()
	c.JSON(ret.Code, ret)
}

// Mpeg1Video 接收 mpeg1vido 数据流
func Mpeg1Video(c *gin.Context) {
	bodyReader := bufio.NewReader(c.Request.Body)

	for {
		data, err := bodyReader.ReadBytes('\n')
		if err != nil {
			break
		}

		service.WsManager.Groupbroadcast(c.Param("channel"), data)
	}
}

// Wsplay 通过 websocket 播放 mpegts 数据
func Wsplay(c *gin.Context) {
	service.WsManager.RegisterClient(c)
}

func saveVideoFile() {

	// RTSP摄像头地址
	rtspURL := "rtsp://username:password@camera_ip:port/stream"

	// FFmpeg命令，直接复制视频流并保存
	cmd := exec.Command("ffmpeg", "-i", rtspURL, "-c", "copy", "-f", "mp4", "output.mp4")

	// 执行命令
	err := cmd.Run()
	if err != nil {
		log.Fatalf("Error: %v", err)
	}
	log.Println("视频保存成功！")
}

// func SaveFile(c *gin.Context) {
// 	service.RunSaveFileFFMPEG()
// }
