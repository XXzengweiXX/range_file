// Package cmd /*
package cmd

import (
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// DownloadOption 下载选项
type DownloadOption struct {
	Url          string
	SavePath     string
	FileName     string
	Size         int64
	MaxGoroutine int
}

// DownloadInfo 文件信息
type DownloadInfo struct {
	url           string
	savePath      string
	totalSize     int64
	totalParts    int64
	perSize       int64
	finishedParts int64
	canSlice      bool
	detail        []DownloadSlice
}

// DownloadSlice 下载详情
type DownloadSlice struct {
	num    int
	start  int64
	end    int64
	status string
}

var Opt DownloadOption
var client http.Client

// downloadCmd represents the download command
var downloadCmd = &cobra.Command{
	Use:   "download",
	Short: "download",
	Long:  `download file form a url`,
	Run: func(cmd *cobra.Command, args []string) {
		start()
	},
}

func init() {
	client = http.Client{}
	rootCmd.AddCommand(downloadCmd)

	downloadCmd.Flags().StringVarP(&Opt.Url, "url", "u", "", "下载地址")
	downloadCmd.Flags().StringVar(&Opt.SavePath, "path", "./downloads", "保存路径")
	downloadCmd.Flags().StringVar(&Opt.FileName, "name", "", "文件名称")
	downloadCmd.Flags().Int64Var(&Opt.Size, "size", 1, "分片大小,单位M")
	downloadCmd.Flags().IntVar(&Opt.MaxGoroutine, "g-num", runtime.NumCPU()*5, "下载启用最大协程数")
}

// start 启动
func start() {
	//fmt.Printf("%+v\n",Opt)
	if Opt.Url == "" {
		log.Println("url is empty")
		return
	}
	err := createDir(Opt.SavePath)
	if err != nil {
		log.Println("fail to create save path:", err.Error())
		return
	}
	info, err := getDownloadFileInfo(Opt)
	if err != nil {
		log.Println("fail to get url info:", err.Error())
		return
	}
	log.Printf("start download file from %s\n save file to:%s\n total size:%d\n total slices:%d\n", info.url, info.savePath, info.totalSize, info.totalParts)
	//fmt.Printf("%+v\n", info)
	//return
	download(info, Opt.MaxGoroutine)
}

// getDownloadFileInfo 获取下载文件信息
func getDownloadFileInfo(opt DownloadOption) (info *DownloadInfo, err error) {
	req, err := http.NewRequest(http.MethodHead, opt.Url, nil)
	if err != nil {
		return
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	// 获取文件名称
	if opt.FileName == "" {
		opt.FileName = path.Base(opt.Url)
	}
	fileSize := resp.ContentLength
	size := opt.Size * 1024 * 1024
	headerRanges := resp.Header.Get("Accept-Ranges")
	// 文件大小不合法
	if fileSize <= 0 {
		err = fmt.Errorf("wrong file size:%d", fileSize)
		return nil, err
	}
	// 判断是否需要分片下载
	if headerRanges != "bytes" || fileSize <= size {
		// 不可分片下载
		info = &DownloadInfo{
			url:           opt.Url,
			savePath:      opt.SavePath + "/" + opt.FileName,
			totalSize:     fileSize,
			totalParts:    1,
			perSize:       size,
			finishedParts: 0,
		}
	} else {
		// 可以分片下载
		info = &DownloadInfo{
			url:           opt.Url,
			savePath:      opt.SavePath + "/" + opt.FileName,
			totalSize:     fileSize,
			totalParts:    (fileSize + size - 1) / size,
			perSize:       size,
			finishedParts: 0,
			canSlice:      true,
		}
	}
	detail := make([]DownloadSlice, 0, info.totalParts)
	for i := 0; i < int(info.totalParts); i++ {
		item := DownloadSlice{
			num:    i + 1,
			end:    info.perSize * int64(i+1),
			status: "prepare",
		}
		if i == 0 {
			item.start = 0
		} else {
			item.start = info.perSize*int64(i) + 1
		}
		detail = append(detail, item)
	}
	info.detail = detail
	return
}

// download 根据分片数据进行下载
func download(info *DownloadInfo, maxGoroutine int) {
	file, err := os.Create(info.savePath)
	if err != nil {
		return
	}
	defer file.Close()
	wg := sync.WaitGroup{}
	// 切片下载错误标识
	var errFlag bool
	var errMsg error
	wg.Add(int(info.totalParts))
	downloadChan := make(chan DownloadSlice, Opt.MaxGoroutine)

	go func() {
		for _, v := range info.detail {
			item := v
			downloadChan <- item
		}
	}()
	// 多协程下载文件
	for i := 0; i < maxGoroutine; i++ {
		go func() {
			for {
				select {
				case downloadItem := <-downloadChan:

					tryTimes := 3
					var downloadErr error
					// 下载错误,进行重试
					for i := 1; i <= tryTimes; i++ {
						downloadErr = downloadSlice(file, info.url, downloadItem, info.canSlice)
						if err != nil {
							time.Sleep(time.Millisecond * 100 * time.Duration(i))
							continue
						}
					}
					if downloadErr != nil {
						downloadItem.status = "failed"
						//fmt.Printf("download file[%s][%d] failed:%s\n", info.url, downloadItem.num, err.Error())
						log.Printf("download file[%s][%d] failed:%s\n", info.url, downloadItem.num, err.Error())
						errFlag = true
						errMsg = downloadErr
					} else {
						downloadItem.status = "finished"
						//fmt.Printf("download file[%s][%d] succeed\n", info.url, downloadItem.num)
						log.Printf("download file[%s][%d] succeed\n", info.url, downloadItem.num)
					}

					atomic.AddInt64(&info.finishedParts, 1)
					wg.Done()
				}
			}
		}()
	}

	wg.Wait()
	// 出现错误,清理下载文件
	if errFlag {
		log.Printf("文件[%s]下载错误:%s\n", info.url, errMsg.Error())
		if err = os.Remove(info.savePath); err != nil {
			log.Printf("错误文件[%s]清理失败:%s\n", info.savePath, err.Error())
		}
	}
}

// downloadSlice 分批次下载写入文件
func downloadSlice(file *os.File, url string, infoSlice DownloadSlice, isSlice bool) (err error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return
	}
	if isSlice {
		req.Header.Set("Range", fmt.Sprintf("bytes=%v-%v", infoSlice.start, infoSlice.end))
	}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	//fmt.Printf("slice response code:%v,header:%+v\n",resp.StatusCode,resp.Header)
	expectStatus := http.StatusPartialContent
	if !isSlice {
		expectStatus = http.StatusOK
	}
	if resp.StatusCode != expectStatus {
		err = fmt.Errorf("expect status code:%d,but get %d", expectStatus, resp.StatusCode)
	}

	// 分批写入文件,
	bufSize := 1024 * 1024

	buf := make([]byte, bufSize)
	fileOffset := infoSlice.start
	for {
		readN, err := resp.Body.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			//fmt.Printf("分批读取内容失败:%s\n", err.Error())
			return err
		}
		//_,err=writer.Write(buf[:readN])
		_, err = file.WriteAt(buf[:readN], fileOffset)
		if err != nil {
			//fmt.Printf("分批写入内容失败:%s\n", err.Error())
			return err
		}
		fileOffset += int64(readN)
	}
	return
}

// fileExited 文件是否存在
func fileExited(filePath string) (exited bool) {
	_, err := os.Stat(filePath)
	if err == nil || os.IsExist(err) {
		exited = true
		return
	}
	return
}

// createDir 创建目录
func createDir(pathName string) (err error) {
	if !fileExited(pathName) {
		err = os.MkdirAll(pathName, os.ModePerm)
	}
	return
}
