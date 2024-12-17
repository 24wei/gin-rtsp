package service

import (
	"errors"
	"fmt"
	"ginrtsp/serializer"
	"ginrtsp/util"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	"time"

	"path"
	"strconv"

	uuid "github.com/satori/go.uuid"
)

// RTSPTransSrv RTSP 转换服务 struct
type RTSPTransSrv struct {
	URL string `form:"url" json:"url" binding:"required,min=1"`
}

type RTSPSaveSrv struct {
	URL string `form:"url" json:"url" binding:"required,min=1"`
}

var (
	// processMap FFMPEG 进程刷新通道，未在指定时间刷新的流将会被关闭
	processMap sync.Map

	// saveFileProcessMap FFMPEG 进程刷新通道，未在指定时间刷新的流将会被关闭
	saveFileProcessMap sync.Map

	// 存储 `chan` 的 Map，用于 externalSignal
	signalMap sync.Map
)

// Service RTSP 转换服务
func (service *RTSPTransSrv) Service() *serializer.Response {
	simpleString := strings.Replace(service.URL, "//", "/", 1)
	splitList := strings.Split(simpleString, "/")

	if splitList[0] != "rtsp:" && len(splitList) < 2 {
		return &serializer.Response{
			Code: 400,
			Msg:  "不是有效的 RTSP 地址",
		}
	}

	// 多个客户端需要播放相同的RTSP流地址时，保证返回WebSocket地址相同
	// 为了支持同一IP多路摄像头，使用simpleString作为hash参数，而不是splitList[1]
	processCh := uuid.NewV3(uuid.NamespaceURL, simpleString).String()
	if ch, ok := processMap.Load(processCh); ok {
		*ch.(*chan struct{}) <- struct{}{}
	} else {
		reflush := make(chan struct{})
		if cmd, stdin, err := runFFMPEG(service.URL, processCh); err != nil {
			return serializer.Err(400, err.Error(), err)
		} else {
			go keepFFMPEG(cmd, stdin, &reflush, processCh)
		}
	}

	playURL := fmt.Sprintf("/stream/live/%s", processCh)
	return serializer.BuildRTSPPlayPathResponse(playURL)
}

func (service *RTSPSaveSrv) SaveService() *serializer.Response {
	simpleString := strings.Replace(service.URL, "//", "/", 1)
	splitList := strings.Split(simpleString, "/")

	if splitList[0] != "rtsp:" && len(splitList) < 2 {
		return &serializer.Response{
			Code: 400,
			Msg:  "不是有效的 RTSP 地址",
		}
	}

	// 多个客户端需要播放相同的RTSP流地址时，保证返回WebSocket地址相同
	// 为了支持同一IP多路摄像头，使用simpleString作为hash参数，而不是splitList[1]
	processCh := uuid.NewV3(uuid.NamespaceURL, simpleString).String()
	if ch, ok := saveFileProcessMap.Load(processCh); ok {
		*ch.(*chan struct{}) <- struct{}{}
	} else {
		reflush := make(chan struct{})
		if cmd, stdin, err := RunSaveFileFFMPEG(service.URL); err != nil {
			return serializer.Err(400, err.Error(), err)
		} else {
			go keepSaveFileFFMPEG(cmd, stdin, &reflush, processCh)
		}
	}

	playURL := fmt.Sprintf("/stream/live/%s", processCh)
	return serializer.BuildRTSPPlayPathResponse(playURL)
}

func keepFFMPEG(cmd *exec.Cmd, stdin io.WriteCloser, ch *chan struct{}, playCh string) {
	processMap.Store(playCh, ch)
	defer func() {
		processMap.Delete(playCh)
		close(*ch)
		_ = stdin.Close()
		util.Log().Info("Stop translate rtsp id %v", playCh)
	}()

	for {
		select {
		case <-*ch:
			util.Log().Info("Reflush channel %s", playCh)

		case <-time.After(60 * time.Second):
			_, _ = stdin.Write([]byte("q"))
			err := cmd.Wait()
			if err != nil {
				util.Log().Error("Run ffmpeg err %v", err.Error())
			}
			return
		}
	}
}

func runFFMPEG(rtsp string, playCh string) (*exec.Cmd, io.WriteCloser, error) {
	port := "3000"
	if len(os.Getenv("RTSP_PORT")) > 0 {
		port = os.Getenv("RTSP_PORT")
	}
	params := []string{
		"-rtsp_transport",
		"tcp",
		"-re",
		"-stimeout",
		"5000000",
		"-i",
		rtsp,
		"-q",
		"5",
		"-f",
		"mpegts",
		"-fflags",
		"nobuffer",
		"-c:v",
		"mpeg1video",
		"-an",
		"-s",
		"960x540",
		fmt.Sprintf("http://127.0.0.1:%s/stream/upload/%s", port, playCh),
	}

	util.Log().Debug("FFmpeg cmd: ffmpeg %v", strings.Join(params, " "))
	cmd := exec.Command("ffmpeg", params...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	stdin, err := cmd.StdinPipe()
	if err != nil {
		util.Log().Error("Get ffmpeg stdin err:%v", err.Error())
		return nil, nil, errors.New("拉流进程启动失败")
	}

	err = cmd.Start()
	if err != nil {
		util.Log().Info("Start ffmpeg err: %v", err.Error())
		return nil, nil, errors.New("打开摄像头视频流失败")
	}
	util.Log().Info("Translate rtsp %v to %v", rtsp, playCh)
	return cmd, stdin, nil
}

func keepSaveFileFFMPEG(cmd *exec.Cmd, stdin io.WriteCloser, ch *chan struct{}, playCh string) {
	saveFileProcessMap.Store(playCh, ch)
	defer func() {
		saveFileProcessMap.Delete(playCh)
		close(*ch)
		_ = stdin.Close()
		util.Log().Info("Stop translate rtsp id %v", playCh)
	}()

	// 获取外部信号通道
	externalCh := externalSignal(playCh)

	for {
		select {
		case <-*ch:
			util.Log().Info("Reflush channel %s", playCh)

		case <-externalCh:
			fmt.Printf("External signal received for %s\n", playCh)
			_, _ = stdin.Write([]byte("q"))
			err := cmd.Wait()
			if err != nil {
				util.Log().Error("Run ffmpeg err %v", err.Error())
			}
			return

		case <-time.After(60 * time.Second):
			_, _ = stdin.Write([]byte("q"))
			err := cmd.Wait()
			if err != nil {
				util.Log().Error("Run ffmpeg err %v", err.Error())
			}
			return
		}
	}
}

func externalSignal(playCh string) <-chan struct{} {
	signalCh := make(chan struct{})
	signalMap.Store(playCh, signalCh)
	return signalCh
}

func RunSaveFileFFMPEG(rtsp string) (*exec.Cmd, io.WriteCloser, error) {
	dir := path.Join("test", time.Now().Format("20060102"))
	// 确保目录存在
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		util.Log().Info("Failed to create directory: %v", err)
		return nil, nil, fmt.Errorf("failed to create directory: %w", err) // 如果目录创建失败，则不继续执行
	}

	m3u8path := path.Join(dir, fmt.Sprintf("out.m3u8"))

	// rtsp := "rtsp://admin:9394974czw@192.168.1.60:554/stream1"
	paramStr := "-c:v copy -c:a aac"
	ts_duration_second := 5
	params := []string{"-fflags", "genpts", "-rtsp_transport", "tcp", "-i", rtsp, "-hls_time", strconv.Itoa(ts_duration_second), "-hls_list_size", "0", m3u8path}
	if paramStr != "default" {
		paramsOfThisPath := strings.Split(paramStr, " ")
		params = append(params[:6], append(paramsOfThisPath, params[6:]...)...)
	}
	// ffmpeg -i ~/Downloads/720p.mp4 -s 640x360 -g 15 -c:a aac -hls_time 5 -hls_list_size 0 record.m3u8
	cmd := exec.Command("ffmpeg", params...)
	f, err := os.OpenFile(path.Join(dir, fmt.Sprintf("log.txt")), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		util.Log().Info("Failed to open log file: %v", err)
		return nil, nil, fmt.Errorf("failed to open log file: %w", err)
	}
	// 设置日志输出
	cmd.Stdout = f
	cmd.Stderr = f

	// 创建管道以写入 stdin
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		util.Log().Info("Failed to create stdin pipe: %v", err)
		f.Close() // 关闭日志文件
		return nil, nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	err = cmd.Start()
	if err != nil {
		util.Log().Info("Start ffmpeg err:%v", err)
		f.Close()         // 如果启动失败，关闭日志文件
		stdinPipe.Close() // 如果启动失败，关闭管道
		return nil, nil, fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	return cmd, stdinPipe, nil
}
