package sources

import "os"

// statPath 判断来源文件是否存在。目录或文件都算「探测到」，由各自解析器决定后续。
func statPath(path string) (os.FileInfo, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	return os.Stat(path)
}
