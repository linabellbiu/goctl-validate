package validator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/linabellbiu/goctl-validate/internal/processor"

	"github.com/zeromicro/go-zero/tools/goctl/plugin"
)

// ProcessPlugin 处理插件逻辑
func ProcessPlugin(p *plugin.Plugin, options processor.Options) error {
	// 构建types目录路径
	typesDir := filepath.Join(p.Dir, "internal", "types")

	// 检查types目录是否存在
	if _, err := os.Stat(typesDir); os.IsNotExist(err) {
		if options.DebugMode {
			fmt.Printf("types目录不存在: %s\n", typesDir)
		}
		return nil
	}

	// 遍历types目录下的所有go文件
	err := filepath.Walk(typesDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// 打印当前处理的文件路径
		fmt.Println("当前处理文件:", path)

		// 只处理go文件，并过滤掉validation.go和translator.go
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".go") {
			// 过滤掉validation.go和translator.go文件
			fileName := info.Name()
			if fileName == "validation.go" || fileName == "translator.go" {
				if options.DebugMode {
					fmt.Printf("跳过文件: %s\n", path)
				}
				return nil
			}

			if options.DebugMode {
				fmt.Printf("处理go文件: %s\n", path)
			}
			if err := processor.ProcessTypesFile(path, options); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}
