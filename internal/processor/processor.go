package processor

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Options 定义处理器的选项
type Options struct {
	// 是否添加自定义验证方法
	EnableCustomValidation bool
	// 是否启用调试模式
	DebugMode bool
	// 是否启用翻译器功能
	EnableTranslator bool
}

// 验证器常量
const (
	ValidateImport = `"github.com/go-playground/validator/v10"`
	ValidateVar    = `var validate = validator.New()`

	// 验证方法映射注释和开始部分
	ValidationRegisterComment = `// registerValidation 存储所有的验证方法
// key: 验证标签名称，value: 对应的验证函数`

	// 验证方法映射
	ValidateRegisterMap = `var registerValidation = map[string]validator.Func{
	"mobile": validateMobile, // 手机号验证
	"idcard": validateIdCard, // 身份证号验证
`

	// 自定义验证方法映射模板
	CustomValidationMapTemplate = `	"%s": validate%s, // %s
`

	// 验证方法注册初始化
	ValidateInitFunc = `
// 初始化并注册所有验证方法
func init() {
	// 遍历注册所有验证方法
	for tag, handler := range registerValidation {
		_ = validate.RegisterValidation(tag, handler)
	}
}
`

	// 自定义验证方法定义模板
	CustomValidationFuncTemplate = `
// 自定义验证方法: %s
func validate%s(fl validator.FieldLevel) bool {
	// 在这里实现 %s 的验证逻辑
	return true
}
`

	// 内置验证方法
	BuiltInValidationFunc = `
// 验证手机号
func validateMobile(fl validator.FieldLevel) bool {
	mobile := fl.Field().String()
	// 使用正则表达式验证中国大陆手机号(13,14,15,16,17,18,19开头的11位数字)
	match, _ := regexp.MatchString("^1[3-9]\\d{9}$", mobile)
	return match
}

// 验证身份证号
func validateIdCard(fl validator.FieldLevel) bool {
	idCard := fl.Field().String()
	// 支持15位或18位身份证号
	match, _ := regexp.MatchString("(^\\d{15}$)|(^\\d{18}$)|(^\\d{17}(\\d|X|x)$)", idCard)
	return match
}
`

	// 翻译器相关导入
	TranslatorImports = `
	"github.com/go-playground/locales/en"
	"github.com/go-playground/locales/zh"
	ut "github.com/go-playground/universal-translator"
	zhTrans "github.com/go-playground/validator/v10/translations/zh"
`
	// 自定义标签翻译注册模板
	CustomTranslationTemplate = `
	_ = trans.Add("%s", "{0}%s", false)
	_ = validate.RegisterTranslation("%s", trans, func(ut ut.Translator) error {
		return nil
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T("%s", fe.Field())
		return t
	})
`
)

// ProcessTypesFile 处理types.go文件，添加验证逻辑
func ProcessTypesFile(filePath string, options Options) error {
	// 读取文件内容
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取文件失败: %w", err)
	}

	if options.DebugMode {
		fmt.Println("============= 原始文件内容 =============")
		fmt.Println(string(fileContent))
		fmt.Println("=======================================")
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, fileContent, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("解析文件失败: %w", err)
	}

	// 寻找所有的请求结构体并生成验证方法
	var reqStructs []string
	// 定义变量，但不使用，防止编译错误
	existingValidations := make(map[string]bool)

	// 检查imports
	hasValidatorImport := false
	for _, imp := range f.Imports {
		if imp.Path.Value == ValidateImport {
			hasValidatorImport = true
			break
		}
	}

	// 提取自定义验证标签
	customTags := make(map[string]bool)

	// 收集所有请求结构体和自定义验证标签
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			// 如果是结构体类型
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}

			// 不再仅限于以Req结尾的结构体
			// 检查所有结构体是否包含validate标签
			hasValidateTag := false
			for _, field := range structType.Fields.List {
				if field.Tag != nil {
					tag := field.Tag.Value
					validateTag := extractValidateTag(tag)
					if validateTag != "" {
						hasValidateTag = true
						break
					}
				}
			}

			// 如果结构体包含验证标签或是以Req结尾，则处理
			if hasValidateTag || strings.HasSuffix(typeSpec.Name.Name, "Req") {
				reqStructs = append(reqStructs, typeSpec.Name.Name)

				// 分析结构体字段的验证标签
				for _, field := range structType.Fields.List {
					if field.Tag != nil {
						tag := field.Tag.Value

						// 提取验证标签
						validateTag := extractValidateTag(tag)
						if validateTag != "" {
							// 分析验证标签中的自定义验证器
							validators := strings.Split(validateTag, ",")
							for _, v := range validators {
								// 跳过空验证器
								if v == "" {
									continue
								}

								// 如果启用了自定义验证或翻译器，添加自定义标签
								if (options.EnableCustomValidation || options.EnableTranslator) && !isBuiltInValidator(v) {
									// 添加自定义验证标签
									customTags[v] = true

									// 如果启用了自定义验证，检查该验证器函数是否已存在
									if options.EnableCustomValidation {
										if bytes.Contains(fileContent, []byte(fmt.Sprintf("func validate%s", strings.Title(v)))) {
											existingValidations[v] = true
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// 没有找到请求结构体，直接返回
	if len(reqStructs) == 0 && len(customTags) == 0 {
		return nil
	}

	// 获取文件所在的目录路径
	dirPath := filepath.Dir(filePath)

	// 验证文件的路径（与types.go在同一目录）
	validationFilePath := filepath.Join(dirPath, "validation.go")

	// 翻译器文件的路径（与types.go在同一目录）
	translatorFilePath := ""
	if options.EnableTranslator {
		translatorFilePath = filepath.Join(dirPath, "translator.go")
	}

	// 检查验证文件是否已存在
	validationExists := false
	validationContent := ""

	if _, err := os.Stat(validationFilePath); err == nil {
		// 验证文件已存在，读取内容
		validationBytes, err := os.ReadFile(validationFilePath)
		if err != nil {
			return fmt.Errorf("读取现有验证文件失败: %w", err)
		}
		validationContent = string(validationBytes)
		validationExists = true

		// 检查现有验证文件中的验证函数
		for tag := range customTags {
			if bytes.Contains(validationBytes, []byte(fmt.Sprintf("func validate%s", strings.Title(tag)))) {
				existingValidations[tag] = true
			}
		}
	}

	// 检查翻译器文件是否已存在
	translatorExists := false

	if options.EnableTranslator && translatorFilePath != "" {
		if _, err := os.Stat(translatorFilePath); err == nil {
			translatorExists = true
		}
	}

	// 获取包名
	packageName := f.Name.Name

	// 生成验证文件内容
	var validationFileContent strings.Builder

	// 如果文件不存在，添加基本结构
	if !validationExists {
		validationFileContent.WriteString(fmt.Sprintf("package %s\n\n", packageName))

		// 添加导入
		validationFileContent.WriteString("import (\n")
		validationFileContent.WriteString("\t\"regexp\"\n")
		validationFileContent.WriteString("\t" + ValidateImport + "\n")
		validationFileContent.WriteString(")\n\n")

		// 添加验证方法映射注释
		validationFileContent.WriteString(ValidationRegisterComment + "\n")

		// 添加验证方法映射开始
		validationFileContent.WriteString(ValidateRegisterMap)

		// 按字母顺序排序标签，确保生成顺序一致
		var sortedTags []string
		for tag := range customTags {
			sortedTags = append(sortedTags, tag)
		}
		sort.Strings(sortedTags)

		// 如果启用了自定义验证，添加自定义验证标签
		if options.EnableCustomValidation && len(customTags) > 0 {
			for _, tag := range sortedTags {
				validationFileContent.WriteString(fmt.Sprintf(CustomValidationMapTemplate, tag, strings.Title(tag), tag))
			}
		}

		// 结束map定义
		validationFileContent.WriteString("}\n")

		validationFileContent.WriteString("\n\n" + ValidateVar + "\n")

		// 添加init函数
		validationFileContent.WriteString(ValidateInitFunc + "\n")

		// 添加内置验证函数
		validationFileContent.WriteString(BuiltInValidationFunc + "\n")

		// 如果启用了自定义验证，添加自定义验证函数
		if options.EnableCustomValidation && len(customTags) > 0 {
			// 按字母顺序添加验证函数
			for _, tag := range sortedTags {
				if !existingValidations[tag] {
					validationFileContent.WriteString(fmt.Sprintf(CustomValidationFuncTemplate, tag, strings.Title(tag), tag))
				}
			}
		}
	} else {
		// 文件已存在，需要更新
		// 1. 提取现有的验证函数和注册
		existingFuncs := make(map[string]bool)
		existingRegs := make(map[string]bool)
		existingRegLines := make(map[string]string) // 存储原始的注册行，用于保持注释一致性

		// 提取文件中所有的验证函数和注册信息
		funcRegex := regexp.MustCompile(`func validate(\w+)\(fl validator\.FieldLevel\) bool`)
		regRegex := regexp.MustCompile(`\t"(\w+)":\s*validate\w+,.*`)

		// 查找所有的验证函数
		funcMatches := funcRegex.FindAllStringSubmatch(validationContent, -1)
		for _, match := range funcMatches {
			if len(match) > 1 {
				// 提取函数名，如AgeRange，变为小写作为tag
				funcName := match[1]
				if funcName != "Mobile" && funcName != "IdCard" { // 跳过内置函数
					tag := strings.ToLower(funcName[0:1]) + funcName[1:]
					existingFuncs[tag] = true
				}
			}
		}

		// 查找所有的注册行和对应的tag
		regMatches := regRegex.FindAllStringSubmatchIndex(validationContent, -1)
		for _, matchIndex := range regMatches {
			if len(matchIndex) >= 4 {
				startOfLine := validationContent[matchIndex[0]:matchIndex[1]]
				tag := validationContent[matchIndex[2]:matchIndex[3]]

				if tag != "mobile" && tag != "idcard" { // 跳过内置标签
					existingRegs[tag] = true
					existingRegLines[tag] = startOfLine // 保存整行内容
				}
			}
		}

		// 2. 收集所有标签，按字母顺序排序
		var allTags []string

		// 添加内置标签(固定顺序)
		allTags = append(allTags, "mobile", "idcard")

		// 收集所有自定义标签
		for tag := range customTags {
			if tag != "mobile" && tag != "idcard" {
				allTags = append(allTags, tag)
			}
		}

		// 收集现有但不在customTags中的标签
		for tag := range existingRegs {
			if tag != "mobile" && tag != "idcard" && !customTags[tag] {
				allTags = append(allTags, tag)
			}
		}

		// 除了内置标签外，对自定义标签按字母排序
		if len(allTags) > 2 {
			sort.Strings(allTags[2:])
		}

		// 3. 生成新的验证方法映射
		var newMapContent strings.Builder
		// 添加验证映射注释
		newMapContent.WriteString(ValidationRegisterComment + "\n")
		newMapContent.WriteString("var registerValidation = map[string]validator.Func{\n")

		// 按排序后的标签顺序添加
		for _, tag := range allTags {
			if tag == "mobile" {
				newMapContent.WriteString("\t\"mobile\": validateMobile, // 手机号验证\n")
			} else if tag == "idcard" {
				newMapContent.WriteString("\t\"idcard\": validateIdCard, // 身份证号验证\n")
			} else {
				// 如果存在原始的注册行，使用它保持格式一致
				if line, exists := existingRegLines[tag]; exists {
					newMapContent.WriteString(line + "\n")
				} else {
					// 否则使用标准格式
					newMapContent.WriteString(fmt.Sprintf(CustomValidationMapTemplate, tag, strings.Title(tag), tag))
				}
			}
		}

		newMapContent.WriteString("}\n")

		// 4. 检查所有缺失的验证函数
		// 为缺失的验证函数创建内容
		var missingFuncContent strings.Builder
		var missingTags []string

		// 收集所有需要验证函数但尚未存在的标签
		for tag := range customTags {
			if !existingFuncs[tag] {
				missingTags = append(missingTags, tag)
			}
		}

		// 按字母顺序添加验证函数
		sort.Strings(missingTags)
		for _, tag := range missingTags {
			missingFuncContent.WriteString(fmt.Sprintf(CustomValidationFuncTemplate, tag, strings.Title(tag), tag))
		}

		// 5. 替换原有的验证方法映射和init函数
		// 首先替换注释和map声明部分
		commentAndMapPattern := `(?s)// registerValidation.*?var registerValidation = map\[string\]validator\.Func\{.*?\}`
		mapRegex := regexp.MustCompile(commentAndMapPattern)

		var newValidationContent string
		if mapRegex.MatchString(validationContent) {
			// 如果已经有map格式了，替换它
			newValidationContent = mapRegex.ReplaceAllString(validationContent, newMapContent.String())

			// 移除validate变量的声明(如果存在)
			//validateVarPattern := `var validate = validator\.New\(\)\n*`
			//validateVarRegex := regexp.MustCompile(validateVarPattern)
			//newValidationContent = validateVarRegex.ReplaceAllString(newValidationContent, "")

			// 添加缺失的验证函数到文件末尾
			if missingFuncContent.Len() > 0 {
				newValidationContent = newValidationContent + "\n" + missingFuncContent.String()
			}
		} else {
			// 如果是旧格式或者格式不匹配，创建一个全新的内容
			var newFullContent strings.Builder
			newFullContent.WriteString(fmt.Sprintf("package %s\n\n", packageName))

			// 添加导入
			newFullContent.WriteString("import (\n")
			newFullContent.WriteString("\t\"regexp\"\n")
			newFullContent.WriteString("\t" + ValidateImport + "\n")
			newFullContent.WriteString(")\n\n")

			// 添加验证方法映射（不添加validator变量）
			newFullContent.WriteString(newMapContent.String() + "\n")

			// 添加 var validate = validator.New()
			newFullContent.WriteString("\n\n" + ValidateVar + "\n")

			// 添加init函数
			newFullContent.WriteString(ValidateInitFunc + "\n")

			// 添加内置验证函数
			newFullContent.WriteString(BuiltInValidationFunc + "\n")

			// 提取所有自定义验证函数
			customFuncPattern := `(?s)// 自定义验证方法:.*?return true\n\}`
			customFuncRegex := regexp.MustCompile(customFuncPattern)
			customFuncMatches := customFuncRegex.FindAllString(validationContent, -1)

			// 按字母顺序整理自定义验证函数
			type FuncInfo struct {
				Tag  string
				Code string
			}
			var funcInfos []FuncInfo

			// 收集所有现有的函数
			for _, funcCode := range customFuncMatches {
				funcNameRegex := regexp.MustCompile(`func validate(\w+)\(`)
				nameMatch := funcNameRegex.FindStringSubmatch(funcCode)
				if len(nameMatch) > 1 {
					funcName := nameMatch[1]
					tag := strings.ToLower(funcName[:1]) + funcName[1:]
					funcInfos = append(funcInfos, FuncInfo{Tag: tag, Code: funcCode})
				}
			}

			// 对函数按标签名排序
			sort.Slice(funcInfos, func(i, j int) bool {
				return funcInfos[i].Tag < funcInfos[j].Tag
			})

			// 添加所有排序后的函数
			for _, funcInfo := range funcInfos {
				newFullContent.WriteString(funcInfo.Code + "\n\n")
			}

			// 添加缺失的验证函数
			for _, tag := range missingTags {
				newFullContent.WriteString(fmt.Sprintf(CustomValidationFuncTemplate, tag, strings.Title(tag), tag))
			}

			newValidationContent = newFullContent.String()
		}

		// 6. 格式化并写入文件
		formatted, err := format.Source([]byte(newValidationContent))
		if err != nil {
			return fmt.Errorf("格式化更新的验证文件代码失败: %w", err)
		}

		if err := os.WriteFile(validationFilePath, formatted, 0o644); err != nil {
			return fmt.Errorf("写入更新的验证文件失败: %w", err)
		}

		if options.DebugMode {
			fmt.Printf("成功更新验证文件: %s\n", validationFilePath)
		}
	}

	// 如果需要翻译器功能，生成翻译器文件
	if options.EnableTranslator && translatorFilePath != "" {
		var translatorFileContent strings.Builder

		// 如果翻译器文件不存在，创建新文件
		if !translatorExists {
			translatorFileContent.WriteString(fmt.Sprintf("package %s\n\n", packageName))

			// 添加导入
			translatorFileContent.WriteString("import (\n")
			translatorFileContent.WriteString("\t\"errors\"\n")
			translatorFileContent.WriteString("\t\"strings\"\n")
			translatorFileContent.WriteString("\t\"github.com/go-playground/validator/v10\"\n")
			translatorFileContent.WriteString(TranslatorImports)
			translatorFileContent.WriteString(")\n\n")

			// 添加翻译器变量
			translatorFileContent.WriteString("var (\n")
			translatorFileContent.WriteString("\tuni      *ut.UniversalTranslator\n")
			translatorFileContent.WriteString("\ttrans    ut.Translator\n")
			translatorFileContent.WriteString(")\n\n")

			// 添加翻译器初始化函数
			translatorFileContent.WriteString("// 初始化翻译器\n")
			translatorFileContent.WriteString("func init() {\n")
			translatorFileContent.WriteString("\t// 初始化翻译器\n")
			translatorFileContent.WriteString("\tenLocale := en.New()\n")
			translatorFileContent.WriteString("\tzhLocale := zh.New()\n")
			translatorFileContent.WriteString("\tuni = ut.New(enLocale, zhLocale)\n\n")
			translatorFileContent.WriteString("\ttrans, _ = uni.GetTranslator(\"zh\")\n")
			translatorFileContent.WriteString("\t// 注册默认翻译\n")
			translatorFileContent.WriteString("\t_ = zhTrans.RegisterDefaultTranslations(validate, trans)\n\n")
			translatorFileContent.WriteString("\t// 注册自定义翻译\n")
			translatorFileContent.WriteString("\tregisterCustomTranslations(validate, trans)\n")
			translatorFileContent.WriteString("}\n\n")

			// 添加错误翻译函数
			translatorFileContent.WriteString("// Translate 翻译验证错误\n")
			translatorFileContent.WriteString("func Translate(err error) error {\n")
			translatorFileContent.WriteString("\tif err == nil {\n")
			translatorFileContent.WriteString("\t\treturn nil\n")
			translatorFileContent.WriteString("\t}\n\n")
			translatorFileContent.WriteString("\tvar errs validator.ValidationErrors\n")
			translatorFileContent.WriteString("\tif ok := errors.As(err, &errs); !ok {\n")
			translatorFileContent.WriteString("\t\treturn err\n")
			translatorFileContent.WriteString("\t}\n\n")
			translatorFileContent.WriteString("\tvar errMsgs []string\n")
			translatorFileContent.WriteString("\tfor _, e := range errs {\n")
			translatorFileContent.WriteString("\t\ttranslatedErr := e.Translate(trans)\n")
			translatorFileContent.WriteString("\t\terrMsgs = append(errMsgs, translatedErr)\n")
			translatorFileContent.WriteString("\t}\n")
			translatorFileContent.WriteString("\t// TODO 可以自定义错误类型\n")
			translatorFileContent.WriteString("\treturn errors.New(strings.Join(errMsgs, \", \"))\n")
			translatorFileContent.WriteString("}\n\n")

			// 添加错误翻译自定义语言
			translatorFileContent.WriteString("// TranslateWithLang 翻译验证错误自定义语言\n")
			translatorFileContent.WriteString("func TranslateWithLang(err error, lang string) error {\n")
			translatorFileContent.WriteString("\tif err == nil {\n")
			translatorFileContent.WriteString("\t\treturn nil\n")
			translatorFileContent.WriteString("\t}\n\n")
			translatorFileContent.WriteString("\tlangtrans, ok := uni.GetTranslator(lang)\n")
			translatorFileContent.WriteString("\tif !ok {\n")
			translatorFileContent.WriteString("\t\tlangtrans = trans\n")
			translatorFileContent.WriteString("\t}\n\n")
			translatorFileContent.WriteString("\tvar errs validator.ValidationErrors\n")
			translatorFileContent.WriteString("\tif ok := errors.As(err, &errs); !ok {\n")
			translatorFileContent.WriteString("\t\treturn err\n")
			translatorFileContent.WriteString("\t}\n\n")
			translatorFileContent.WriteString("\tvar errMsgs []string\n")
			translatorFileContent.WriteString("\tfor _, e := range errs {\n")
			translatorFileContent.WriteString("\t\ttranslatedErr := e.Translate(langtrans)\n")
			translatorFileContent.WriteString("\t\terrMsgs = append(errMsgs, translatedErr)\n")
			translatorFileContent.WriteString("\t}\n")
			translatorFileContent.WriteString("\t// TODO 可以自定义错误类型\n")
			translatorFileContent.WriteString("\treturn errors.New(strings.Join(errMsgs, \", \"))\n")
			translatorFileContent.WriteString("}\n\n")

			// 添加自定义翻译注册函数
			translatorFileContent.WriteString("// 注册自定义翻译\n")
			translatorFileContent.WriteString("func registerCustomTranslations(validate *validator.Validate, trans ut.Translator) {\n")
			translatorFileContent.WriteString("\t// 内置自定义验证器的翻译\n")
			translatorFileContent.WriteString("\t_ = trans.Add(\"mobile\", \"{0}手机号码格式不正确\", true)\n")
			translatorFileContent.WriteString("\t_ = validate.RegisterTranslation(\"mobile\", trans, func(ut ut.Translator) error {\n")
			translatorFileContent.WriteString("\t\treturn nil\n")
			translatorFileContent.WriteString("\t}, func(ut ut.Translator, fe validator.FieldError) string {\n")
			translatorFileContent.WriteString("\t\tt, _ := ut.T(\"mobile\", fe.Field())\n")
			translatorFileContent.WriteString("\t\treturn t\n")
			translatorFileContent.WriteString("\t})\n\n")
			translatorFileContent.WriteString("\t_ = trans.Add(\"idcard\", \"{0}身份证号码格式不正确\", true)\n")
			translatorFileContent.WriteString("\t_ = validate.RegisterTranslation(\"idcard\", trans, func(ut ut.Translator) error {\n")
			translatorFileContent.WriteString("\t\treturn nil\n")
			translatorFileContent.WriteString("\t}, func(ut ut.Translator, fe validator.FieldError) string {\n")
			translatorFileContent.WriteString("\t\tt, _ := ut.T(\"idcard\", fe.Field())\n")
			translatorFileContent.WriteString("\t\treturn t\n")
			translatorFileContent.WriteString("\t})\n")

			// 为自定义标签添加初始翻译
			for tag := range customTags {
				if !isBuiltInValidator(tag) {
					// 为新标签生成默认翻译文本
					var description string
					switch tag {
					case "uuid":
						description = "格式不正确"
					case "datetime":
						description = "日期格式不正确"
					default:
						description = "格式不符合要求"
					}

					translatorFileContent.WriteString(fmt.Sprintf("\n\t_ = trans.Add(\"%s\", \"{0}%s\", true)\n", tag, description))
					translatorFileContent.WriteString(fmt.Sprintf("\t_ = validate.RegisterTranslation(\"%s\", trans, func(ut ut.Translator) error {\n", tag))
					translatorFileContent.WriteString("\t\treturn nil\n")
					translatorFileContent.WriteString("\t}, func(ut ut.Translator, fe validator.FieldError) string {\n")
					translatorFileContent.WriteString(fmt.Sprintf("\t\tt, _ := ut.T(\"%s\", fe.Field())\n", tag))
					translatorFileContent.WriteString("\t\treturn t\n")
					translatorFileContent.WriteString("\t})\n")
				}
			}

			translatorFileContent.WriteString("}\n")

			// 格式化并写入翻译器文件
			formatted, err := format.Source([]byte(translatorFileContent.String()))
			if err != nil {
				return fmt.Errorf("格式化翻译器文件代码失败: %w", err)
			}

			if err := os.WriteFile(translatorFilePath, formatted, 0o644); err != nil {
				return fmt.Errorf("写入翻译器文件失败: %w", err)
			}

			if options.DebugMode {
				fmt.Printf("成功创建翻译器文件: %s\n", translatorFilePath)
			}
		} else {
			// 如果翻译器文件已存在，追加新的自定义标签翻译
			// 读取现有的翻译器文件
			translatorBytes, err := os.ReadFile(translatorFilePath)
			if err != nil {
				return fmt.Errorf("读取现有翻译器文件失败: %w", err)
			}

			translatorContent := string(translatorBytes)

			// 提取已存在的翻译
			existingTranslations := make(map[string]bool)
			transRegex := regexp.MustCompile(`RegisterTranslation\("([^"]+)"`)
			transMatches := transRegex.FindAllStringSubmatch(translatorContent, -1)

			for _, match := range transMatches {
				if len(match) > 1 {
					existingTranslations[match[1]] = true
				}
			}

			if options.DebugMode {
				fmt.Println("现有的翻译标签:", existingTranslations)
				fmt.Println("自定义标签:", customTags)
			}

			// 检查有没有新的自定义标签需要添加翻译
			var newTranslations strings.Builder
			for tag := range customTags {
				if options.DebugMode {
					fmt.Printf("检查标签 %s: 存在于现有翻译=%v, 是内置标签=%v\n",
						tag, existingTranslations[tag], isBuiltInValidator(tag))
				}

				// 仅为非内置标签且未翻译的标签添加翻译
				if !existingTranslations[tag] && !isBuiltInValidator(tag) {
					// 为新标签生成默认翻译文本（可以根据标签名生成合理的中文描述）
					var description string
					switch tag {
					case "uuid":
						description = "格式不正确"
					case "datetime", "date", "time":
						description = "日期格式不正确"
					default:
						description = "格式不符合要求"
					}

					if options.DebugMode {
						fmt.Printf("添加标签 %s 的翻译\n", tag)
					}

					newTranslations.WriteString(fmt.Sprintf(CustomTranslationTemplate, tag, description, tag, tag))
				}
			}

			// 如果有新的翻译，追加到registerCustomTranslations函数末尾
			if newTranslations.Len() > 0 {
				// 找到registerCustomTranslations函数
				funcStartRegex := regexp.MustCompile(`func registerCustomTranslations\([^)]+\) {`)
				funcStartMatch := funcStartRegex.FindStringIndex(translatorContent)

				if funcStartMatch == nil {
					return fmt.Errorf("无法找到registerCustomTranslations函数")
				}

				// 找到函数的开始位置
				funcStart := funcStartMatch[1] // 使用函数声明的结束位置

				// 计算函数体的大括号配对
				braceCount := 1
				funcEnd := -1

				for i := funcStart; i < len(translatorContent); i++ {
					if translatorContent[i] == '{' {
						braceCount++
					} else if translatorContent[i] == '}' {
						braceCount--
						if braceCount == 0 {
							funcEnd = i
							break
						}
					}
				}

				if funcEnd == -1 {
					return fmt.Errorf("无法找到registerCustomTranslations函数的结束位置")
				}

				// 在函数结束位置的大括号前添加新翻译
				modifiedContent := translatorContent[:funcEnd] + newTranslations.String() + translatorContent[funcEnd:]

				if options.DebugMode {
					fmt.Printf("修改后的翻译器内容:\n%s\n", modifiedContent)
				}

				// 尝试格式化代码
				formatted, err := format.Source([]byte(modifiedContent))
				if err != nil {
					// 如果格式化失败，尝试在函数的适当位置添加翻译
					if options.DebugMode {
						fmt.Printf("格式化失败: %v\n", err)
					}

					// 寻找最后一个翻译注册的位置
					lastRegisterPos := strings.LastIndex(translatorContent, "RegisterTranslation(")
					if lastRegisterPos == -1 {
						return fmt.Errorf("无法找到适合添加翻译的位置")
					}

					// 找到此注册的结束位置（下一个}）
					endRegisterPos := strings.Index(translatorContent[lastRegisterPos:], "})") + lastRegisterPos
					if endRegisterPos == -1 {
						return fmt.Errorf("无法找到适合添加翻译的位置")
					}

					// 在此位置后添加新翻译
					endRegisterPos += 2 // 跳过})
					modifiedContent = translatorContent[:endRegisterPos] + "\n" + newTranslations.String() + translatorContent[endRegisterPos:]

					formatted, err = format.Source([]byte(modifiedContent))
					if err != nil {
						return fmt.Errorf("格式化翻译器代码失败: %w", err)
					}
				}

				// 写入更新后的文件
				if err := os.WriteFile(translatorFilePath, formatted, 0o644); err != nil {
					return fmt.Errorf("写入更新的翻译器文件失败: %w", err)
				}

				if options.DebugMode {
					fmt.Printf("成功更新翻译器文件: %s\n", translatorFilePath)
				}
			} else if options.DebugMode {
				fmt.Println("没有需要添加翻译的新标签")
			}
		}
	}

	// 为所有请求结构体生成验证方法
	var methodsBuilder strings.Builder

	// 检查是否需要添加验证器的导入
	if !hasValidatorImport && len(reqStructs) > 0 {
		// 找到最后一个导入
		lastImportPos := -1
		for i, decl := range f.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if ok && genDecl.Tok == token.IMPORT {
				lastImportPos = i
			}
		}

		// 找到文件中的包声明之后的位置
		fileContentStr := string(fileContent)
		packageEndPos := findPackagePosition(fileContentStr)
		if packageEndPos > 0 {
			packageEndPos = packageEndPos + len("package "+f.Name.Name)

			// 如果已经有导入部分
			if lastImportPos >= 0 {
				// 将验证器的导入添加到现有导入部分
				// 实现比较复杂，这里简单处理为在末尾添加
			} else {
				// 在包声明之后添加导入
				//importStatement := "\n\nimport (\n\t" + ValidateImport + "\n)"

				// 插入导入语句
				if options.DebugMode {
					fmt.Println("添加验证器导入")
				}

				// 将导入添加到文件内容
				fileContentStr = fileContentStr[:packageEndPos] + fileContentStr[packageEndPos:]
				fileContent = []byte(fileContentStr)
			}
		}

		fileContentStr = string(fileContent)
		fileContent = []byte(fileContentStr)
	}

	// 根据是否启用翻译器来生成不同的Validate方法
	for _, structName := range reqStructs {
		// 检查是否已经存在该结构体的Validate方法
		if !strings.Contains(string(fileContent), "func (r *"+structName+") Validate()") {
			//if options.EnableTranslator {
			//	// 使用翻译器版本的验证方法
			//	methodsBuilder.WriteString(fmt.Sprintf("\nfunc (r *%s) Validate() error {\n\terr := validate.Struct(r)\n\treturn TranslateError(err)\n}\n", structName))
			//} else {
			// 使用普通版本的验证方法
			methodsBuilder.WriteString(fmt.Sprintf("\nfunc (r *%s) Validate() error {\n\treturn validate.Struct(r)\n}\n", structName))
			//}
		}
	}

	// 将方法添加到types.go文件末尾
	if methodsBuilder.Len() > 0 {
		modifiedContent := string(fileContent) + methodsBuilder.String()

		// 格式化代码
		formatted, err := format.Source([]byte(modifiedContent))
		if err != nil {
			return fmt.Errorf("格式化代码失败: %w", err)
		}

		// 写回文件
		if err := os.WriteFile(filePath, formatted, 0o644); err != nil {
			return fmt.Errorf("写入文件失败: %w", err)
		}

		if options.DebugMode {
			fmt.Printf("成功添加验证方法到 %s\n", filePath)
		}
	}

	// 如果需要创建或更新验证文件
	if !validationExists {
		// 格式化验证文件内容
		formatted, err := format.Source([]byte(validationFileContent.String()))
		if err != nil {
			return fmt.Errorf("格式化验证文件代码失败: %w", err)
		}

		// 写入验证文件
		if err := os.WriteFile(validationFilePath, formatted, 0o644); err != nil {
			return fmt.Errorf("写入验证文件失败: %w", err)
		}

		if options.DebugMode {
			fmt.Printf("成功创建验证文件: %s\n", validationFilePath)
		}
	}

	return nil
}

// 从结构体标签中提取validate标签内容
func extractValidateTag(tag string) string {
	re := regexp.MustCompile(`validate:"([^"]*)"`)
	matches := re.FindStringSubmatch(tag)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// 判断是否是内置验证器
func isBuiltInValidator(validator string) bool {
	builtInValidators := map[string]bool{
		"required":    true,
		"required_if": true,
		"mobile":      true,
		"idcard":      true,
		"email":       true,
		"url":         true,
		"ip":          true,
		"len":         true,
		"min":         true,
		"max":         true,
		"eq":          true,
		"ne":          true,
		"lt":          true,
		"lte":         true,
		"gt":          true,
		"gte":         true,
		"oneof":       true,
		"numeric":     true,
		"alpha":       true,
		"alphanum":    true,
		"omitempty":   true, // 这实际上是JSON标签的一部分，不是验证标签
	}

	// 检查是否是带参数的内置验证器，如min=10
	parts := strings.Split(validator, "=")
	if len(parts) > 1 {
		return builtInValidators[parts[0]]
	}

	return builtInValidators[validator]
}

// findPackagePosition 查找package关键字在文件中的位置
func findPackagePosition(content string) int {
	// 使用正则表达式查找package关键字
	re := regexp.MustCompile(`(?m)^package\s+\w+`)
	loc := re.FindStringIndex(content)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// ProcessTranslator 处理翻译器文件
func ProcessTranslator(filePath string, regFuncFromTypeStruct string, customTags map[string]bool, debugMode bool) error {
	// 获取类型文件所在的目录
	dir := filepath.Dir(filePath)
	translatorFilePath := filepath.Join(dir, "translator.go")

	// 读取现有的翻译器文件
	var translatorContent []byte
	var err error
	translatorExists := false
	if _, err = os.Stat(translatorFilePath); err == nil {
		translatorExists = true
		translatorContent, err = os.ReadFile(translatorFilePath)
		if err != nil {
			return err
		}
	}

	// 创建或更新翻译器文件
	if !translatorExists {
		// 如果翻译器文件不存在，创建一个新的
		translatorCode := generateNewTranslatorCode(customTags)
		return os.WriteFile(translatorFilePath, []byte(translatorCode), 0o644)
	}

	// 更新现有的翻译器文件
	var newTranslations strings.Builder

	// 对于所有自定义标签，添加新的翻译
	for tag := range customTags {
		// 检查此标签是否已有翻译，以及是否为内置验证器
		if debugMode {
			fmt.Printf("[Debug] 检查自定义标签: %s\n", tag)

			// 特别检查此标签是否已有翻译
			hasTranslation := bytes.Contains(translatorContent, []byte(fmt.Sprintf("RegisterTranslation(\"%s\"", tag)))
			fmt.Printf("[Debug] 标签 %s 已有翻译: %v\n", tag, hasTranslation)

			// 检查是否为内置验证器
			isBuiltIn := isBuiltInValidator(tag)
			fmt.Printf("[Debug] 标签 %s 是内置验证器: %v\n", tag, isBuiltIn)
		}

		if !bytes.Contains(translatorContent, []byte(fmt.Sprintf("RegisterTranslation(\"%s\"", tag))) && !isBuiltInValidator(tag) {
			tagDesc := getTagDescription(tag)
			newTranslations.WriteString(fmt.Sprintf(`
	// 注册 %s 验证器的翻译
	v.RegisterTranslation("%s", trans, func(ut ut.Translator) error {
		return ut.Add("%s.%s", "{0} %s", true)
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T("%s.%s", fe.Field())
		return t
	})
`, tag, tag, "validationErrors", tag, tagDesc, "validationErrors", tag))
		}
	}

	// 如果有新的翻译，更新文件
	if newTranslations.Len() > 0 {
		// 寻找最后一个RegisterTranslation调用后的位置
		lastRegisterPos := bytes.LastIndex(translatorContent, []byte("RegisterTranslation("))
		if lastRegisterPos == -1 {
			// 如果找不到RegisterTranslation调用，寻找init函数的结束位置
			initEndPos := bytes.LastIndex(translatorContent, []byte("}"))
			if initEndPos == -1 {
				return fmt.Errorf("无法找到适合插入新翻译的位置")
			}

			// 添加新的翻译
			updatedContent := append(translatorContent[:initEndPos], []byte(newTranslations.String())...)
			updatedContent = append(updatedContent, translatorContent[initEndPos:]...)
			return os.WriteFile(translatorFilePath, updatedContent, 0o644)
		}

		// 找到此RegisterTranslation调用的结束位置
		afterLastRegister := lastRegisterPos
		braceCount := 0
		for i := lastRegisterPos; i < len(translatorContent); i++ {
			if translatorContent[i] == '{' {
				braceCount++
			} else if translatorContent[i] == '}' {
				braceCount--
				if braceCount < 0 && bytes.HasPrefix(translatorContent[i+1:], []byte("\n")) {
					afterLastRegister = i + 1
					break
				}
			}
		}

		// 插入新的翻译
		updatedContent := append(translatorContent[:afterLastRegister], []byte(newTranslations.String())...)
		updatedContent = append(updatedContent, translatorContent[afterLastRegister:]...)
		return os.WriteFile(translatorFilePath, updatedContent, 0o644)
	}

	return nil
}

// 获取标签的描述
func getTagDescription(tag string) string {
	switch tag {
	case "mobile":
		return "必须是有效的手机号码"
	case "idcard":
		return "必须是有效的身份证号码"
	default:
		// 为未知标签提供一个默认描述
		return fmt.Sprintf("必须是有效的 %s 格式", tag)
	}
}

// 为结构体添加验证方法
func AddValidationMethodsToStructs(filePath string, options *Options) error {
	// 读取文件
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	// 解析Go代码
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, fileContent, parser.ParseComments)
	if err != nil {
		return err
	}

	// 检查包名
	packageName := file.Name.Name

	// 收集需要处理的结构体和自定义验证标签
	var reqStructs []string
	customTags := make(map[string]bool)
	existingValidations := make(map[string]bool)

	regFuncFromTypeStruct := ""

	// 遍历AST，找到所有结构体
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}

			// 检测是否有validate标签的字段
			hasValidateTag := false
			for _, field := range structType.Fields.List {
				if field.Tag != nil && strings.Contains(field.Tag.Value, "validate:") {
					hasValidateTag = true
					break
				}
			}

			// 如果结构体包含验证标签或是以Req结尾，则处理
			if hasValidateTag || strings.HasSuffix(typeSpec.Name.Name, "Req") {
				reqStructs = append(reqStructs, typeSpec.Name.Name)

				// 分析结构体字段的验证标签
				for _, field := range structType.Fields.List {
					if field.Tag != nil {
						tag := field.Tag.Value

						// 提取验证标签
						validateTag := extractValidateTag(tag)
						if validateTag != "" {
							// 分析验证标签中的自定义验证器
							validators := strings.Split(validateTag, ",")
							for _, v := range validators {
								// 跳过空验证器
								if v == "" {
									continue
								}

								// 如果启用了自定义验证或翻译器，添加自定义标签
								if (options.EnableCustomValidation || options.EnableTranslator) && !isBuiltInValidator(v) {
									// 添加自定义验证标签
									customTags[v] = true

									// 如果启用了自定义验证，检查该验证器函数是否已存在
									if options.EnableCustomValidation {
										if bytes.Contains(fileContent, []byte(fmt.Sprintf("func validate%s", strings.Title(v)))) {
											existingValidations[v] = true
										}
									}
								}
							}
						}
					}
				}
			}
		}

		// 检查是否已有Validate方法
		if funcDecl, ok := decl.(*ast.FuncDecl); ok && funcDecl.Name.Name == "Validate" {
			if funcDecl.Recv != nil && len(funcDecl.Recv.List) > 0 {
				if starExpr, ok := funcDecl.Recv.List[0].Type.(*ast.StarExpr); ok {
					if ident, ok := starExpr.X.(*ast.Ident); ok {
						regFuncFromTypeStruct = fmt.Sprintf("func (v *%s) Validate() error", ident.Name)
					}
				}
			}
		}
	}

	// 如果没有结构体需要处理，直接返回
	if len(reqStructs) == 0 {
		return nil
	}

	// 生成import语句
	var imports []string
	imports = append(imports, `"github.com/go-playground/validator/v10"`)

	// 添加validator包到imports
	if !bytes.Contains(fileContent, []byte(`"github.com/go-playground/validator/v10"`)) {
		// 查找import部分
		importStart := bytes.Index(fileContent, []byte("import ("))
		importEnd := -1
		if importStart != -1 {
			importEnd = bytes.Index(fileContent[importStart:], []byte(")"))
			if importEnd != -1 {
				importEnd += importStart
			}
		}

		var newContent []byte
		if importStart != -1 && importEnd != -1 {
			// 添加到现有import
			newContent = append(fileContent[:importEnd], []byte("\n\t\"github.com/go-playground/validator/v10\"")...)
			newContent = append(newContent, fileContent[importEnd:]...)
		} else {
			// 创建新的import
			importStmt := "\nimport (\n\t\"github.com/go-playground/validator/v10\"\n)\n"
			packageEnd := bytes.Index(fileContent, []byte("package "+packageName)) + len("package "+packageName)
			newLine := bytes.IndexByte(fileContent[packageEnd:], '\n')
			if newLine != -1 {
				packageEnd += newLine
			}
			newContent = append(fileContent[:packageEnd+1], []byte(importStmt)...)
			newContent = append(newContent, fileContent[packageEnd+1:]...)
		}
		fileContent = newContent
	}

	// 如果启用了翻译器功能，添加相关import
	if options.EnableTranslator {
		if !bytes.Contains(fileContent, []byte(`"github.com/go-playground/locales/zh"`)) {
			// 查找import部分
			importStart := bytes.Index(fileContent, []byte("import ("))
			importEnd := -1
			if importStart != -1 {
				importEnd = bytes.Index(fileContent[importStart:], []byte(")"))
				if importEnd != -1 {
					importEnd += importStart
				}
			}

			var newContent []byte
			if importStart != -1 && importEnd != -1 {
				// 添加到现有import
				importAddition := []byte("\n\t\"github.com/go-playground/locales/en\"\n\t\"github.com/go-playground/locales/zh\"\n\t\"github.com/go-playground/universal-translator\"")
				newContent = append(fileContent[:importEnd], importAddition...)
				newContent = append(newContent, fileContent[importEnd:]...)
			}
			fileContent = newContent
		}
	}

	// 处理翻译器
	if options.EnableTranslator {
		err = ProcessTranslator(filePath, regFuncFromTypeStruct, customTags, options.DebugMode)
		if err != nil {
			return err
		}
	}

	// 添加验证方法
	for _, structName := range reqStructs {
		validateMethod := fmt.Sprintf(`
// Validate 验证 %s 的字段
func (r *%s) Validate() error {
	validate := validator.New()
	return validate.Struct(r)
}
`, structName, structName)

		// 检查结构体是否已有Validate方法
		validateMethodSign := fmt.Sprintf("func (r *%s) Validate() error", structName)
		if !bytes.Contains(fileContent, []byte(validateMethodSign)) {
			fileContent = append(fileContent, []byte(validateMethod)...)
		}
	}

	// 处理自定义验证
	if options.EnableCustomValidation && len(customTags) > 0 {
		// 创建或更新validation.go文件
		dir := filepath.Dir(filePath)
		validationFilePath := filepath.Join(dir, "validation.go")

		// 添加Debug日志
		if options.DebugMode {
			fmt.Printf("[Debug] 处理自定义验证，有 %d 个自定义标签\n", len(customTags))
			for tag := range customTags {
				fmt.Printf("[Debug] 自定义标签: %s，是否已存在验证: %v\n", tag, existingValidations[tag])
			}
		}

		// 检查是否已有validation.go文件
		if _, err := os.Stat(validationFilePath); os.IsNotExist(err) {
			// 生成新的validation.go文件
			validationCode := generateValidationCode(packageName, customTags, existingValidations)
			err = os.WriteFile(validationFilePath, []byte(validationCode), 0o644)
			if err != nil {
				return err
			}
		} else {
			// 更新现有的validation.go文件
			validationContent, err := os.ReadFile(validationFilePath)
			if err != nil {
				return err
			}

			// 为每个缺失的自定义验证器添加验证函数
			var newValidations strings.Builder
			for tag := range customTags {
				if !existingValidations[tag] && !bytes.Contains(validationContent, []byte(fmt.Sprintf("validate%s", strings.Title(tag)))) {
					validationFunc := generateValidationFunction(tag)
					newValidations.WriteString(validationFunc)

					// 添加到init函数中
					initTag := fmt.Sprintf("validate.RegisterValidation(\"%s\", validate%s)", tag, strings.Title(tag))
					if !bytes.Contains(validationContent, []byte(initTag)) {
						// 查找init函数的结尾
						initPos := bytes.Index(validationContent, []byte("func init()"))
						if initPos != -1 {
							// 找到init函数块的大括号
							braceStart := bytes.IndexByte(validationContent[initPos:], '{')
							if braceStart != -1 {
								braceStart += initPos
								braceEnd := findMatchingCloseBrace(validationContent, braceStart)
								if braceEnd != -1 {
									newValidationContent := append(validationContent[:braceEnd], []byte(fmt.Sprintf("\n\tvalidate.RegisterValidation(\"%s\", validate%s)", tag, strings.Title(tag)))...)
									newValidationContent = append(newValidationContent, validationContent[braceEnd:]...)
									validationContent = newValidationContent
								}
							}
						}
					}
				}
			}

			// 添加新的验证函数
			if newValidations.Len() > 0 {
				validationContent = append(validationContent, []byte(newValidations.String())...)
				err = os.WriteFile(validationFilePath, validationContent, 0o644)
				if err != nil {
					return err
				}
			}
		}
	}

	// 保存对types.go文件的修改
	return os.WriteFile(filePath, fileContent, 0o644)
}

// 查找匹配的右括号位置
func findMatchingCloseBrace(content []byte, openBracePos int) int {
	braceCount := 1
	for i := openBracePos + 1; i < len(content); i++ {
		if content[i] == '{' {
			braceCount++
		} else if content[i] == '}' {
			braceCount--
			if braceCount == 0 {
				return i
			}
		}
	}
	return -1
}

// 生成新的翻译器代码
func generateNewTranslatorCode(customTags map[string]bool) string {
	var customTagCode strings.Builder
	for tag := range customTags {
		if !isBuiltInValidator(tag) {
			tagDesc := getTagDescription(tag)
			customTagCode.WriteString(fmt.Sprintf(`
	// 注册 %s 验证器的翻译
	v.RegisterTranslation("%s", trans, func(ut ut.Translator) error {
		return ut.Add("%s.%s", "{0} %s", true)
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T("%s.%s", fe.Field())
		return t
	})
`, tag, tag, "validationErrors", tag, tagDesc, "validationErrors", tag))
		}
	}

	code := fmt.Sprintf(`package types

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-playground/locales/en"
	"github.com/go-playground/locales/zh"
	ut "github.com/go-playground/universal-translator"
	"github.com/go-playground/validator/v10"
	zhTranslations "github.com/go-playground/validator/v10/translations/zh"
)

var (
	uni      *ut.UniversalTranslator
	trans    ut.Translator
	validate *validator.Validate
)

// InitTranslator 初始化验证翻译器
func InitTranslator() {
	// 初始化验证器和翻译器
	validate = validator.New()
	
	// 注册标签翻译
	validate.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return field.Name
		}
		return name
	})

	// 中英文翻译器
	zhCn := zh.New()
	en := en.New()
	uni = ut.New(en, zhCn)

	// 获取中文翻译器
	trans, _ = uni.GetTranslator("zh")

	// 注册默认中文翻译器
	zhTranslations.RegisterDefaultTranslations(validate, trans)

	// 注册自定义标签的翻译
	// 注册 mobile 验证器的翻译
	v.RegisterTranslation("mobile", trans, func(ut ut.Translator) error {
		return ut.Add("validationErrors.mobile", "{0} 必须是有效的手机号码", true)
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T("validationErrors.mobile", fe.Field())
		return t
	})

	// 注册 idcard 验证器的翻译
	v.RegisterTranslation("idcard", trans, func(ut ut.Translator) error {
		return ut.Add("validationErrors.idcard", "{0} 必须是有效的身份证号码", true)
	}, func(ut ut.Translator, fe validator.FieldError) string {
		t, _ := ut.T("validationErrors.idcard", fe.Field())
		return t
	})
%s
}

// Translate 翻译验证错误信息
func Translate(err error) string {
	if err == nil {
		return ""
	}
	
	errs, ok := err.(validator.ValidationErrors)
	if !ok {
		return err.Error()
	}

	var errMsgs []string
	for _, e := range errs {
		translatedErr := e.Translate(trans)
		errMsgs = append(errMsgs, translatedErr)
	}
	
	return strings.Join(errMsgs, ", ")
}
`, customTagCode.String())

	return code
}

// 生成验证函数代码
func generateValidationFunction(tag string) string {
	// 根据标签生成验证函数
	switch tag {
	case "mobile":
		return `
// validateMobile 验证手机号码
func validateMobile(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	// 验证中国大陆手机号码
	reg := regexp.MustCompile(` + "`" + `^1[3-9]\d{9}$` + "`" + `)
	return reg.MatchString(value)
}
`
	case "idcard":
		return `
// validateIdcard 验证身份证号码
func validateIdcard(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	// 验证中国大陆18位身份证号码
	reg := regexp.MustCompile(` + "`" + `^\d{17}[\dXx]$` + "`" + `)
	return reg.MatchString(value)
}
`
	default:
		// 生成一个默认的验证函数
		return fmt.Sprintf(`
// validate%s 验证%s格式
func validate%s(fl validator.FieldLevel) bool {
	// 请实现此验证函数...
	// 此为自动生成的验证函数，需要手动实现验证逻辑
	return true
}
`, strings.Title(tag), tag, strings.Title(tag))
	}
}

// 生成完整的验证代码
func generateValidationCode(packageName string, customTags map[string]bool, existingValidations map[string]bool) string {
	var registrations strings.Builder
	var validationFunctions strings.Builder

	for tag := range customTags {
		if !existingValidations[tag] {
			registrations.WriteString(fmt.Sprintf("\tvalidate.RegisterValidation(\"%s\", validate%s)\n", tag, strings.Title(tag)))
			validationFunctions.WriteString(generateValidationFunction(tag))
		}
	}

	code := fmt.Sprintf(`package %s

import (
	"regexp"

	"github.com/go-playground/validator/v10"
)

var validate *validator.Validate

func init() {
	validate = validator.New()
%s}
%s
`, packageName, registrations.String(), validationFunctions.String())

	return code
}
