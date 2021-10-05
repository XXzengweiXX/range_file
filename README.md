# range_file
分段下载远程文件

## 参数说明
```shell
-h 帮助
date 查看当前时间
download 下载命令
# 子命令
      --g-num int     下载启用最大协程数 (default cpu数量的5倍)
  -h, --help          help for download
      --name string   文件名称
      --path string   保存路径 (default "./downloads")
      --size int      分片大小,单位M (default 1)
  -u, --url string    下载地址
```
