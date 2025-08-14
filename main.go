package main

import (
	"fmt"
	"os"

	"github.com/linabellbiu/goctl-validate/internal/processor"
	"github.com/linabellbiu/goctl-validate/internal/validator"

	"github.com/spf13/cobra"
	"github.com/zeromicro/go-zero/tools/goctl/plugin"
)

var (
	// 版本信息
	version = "1.1.1"
	// 是否添加自定义验证方法
	enableCustomValidation bool
	// 是否启用调试模式
	debugMode bool
	// 是否启用翻译器功能
	enableTranslator bool

	rootCmd = &cobra.Command{
		Use:     "validate",
		Short:   "A goctl plugin to generate validation code for API types",
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := plugin.NewPlugin()
			if err != nil {
				return err
			}

			// 设置处理选项
			options := processor.Options{
				EnableCustomValidation: enableCustomValidation,
				DebugMode:              debugMode,
				EnableTranslator:       enableTranslator,
			}

			return validator.ProcessPlugin(p, options)
		},
	}
)

func init() {
	rootCmd.Flags().BoolVar(&enableCustomValidation, "custom", false, "Enable custom validation methods")
	rootCmd.Flags().BoolVar(&debugMode, "debug", false, "Enable debug mode")
	rootCmd.Flags().BoolVar(&enableTranslator, "translator", false, "Enable validation error translator (default: Chinese)")
}

func main() {
	// 处理 goctl 插件参数格式
	validator.ParsePluginArgs()
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}
