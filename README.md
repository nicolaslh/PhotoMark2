# 相片打印助手

基于 Wails v3、Go、Vue3、TypeScript、TailwindCSS 的跨平台桌面应用初始框架。当前版本实现了图片导入预览、Go 后端 EXIF 解析、Canvas 水印拖拽、相纸比例切换、非破坏性保存与系统打印桥接。

## 目录结构

```text
.
├── main.go                  # Wails v3 应用入口与 PhotoService 绑定
├── photo_service.go         # 图片读取、合成图保存、打印入口
├── exif_parser.go           # 无外部 EXIF 依赖的 JPEG/TIFF 元数据解析
├── image_config.go          # 图片尺寸解析，支持 JPEG/PNG/GIF/BMP/TIFF/WebP
├── print_darwin.go          # macOS 打印桥接
├── print_windows.go         # Windows 打印桥接
├── print_linux.go           # Linux 打印桥接
├── frontend/
│   ├── src/App.vue          # Canvas 预览、水印拖拽、相纸适配、打印 UI
│   ├── src/style.css        # Tailwind 入口与全局样式
│   ├── bindings/            # wails3 generate bindings 生成的 TS 绑定
│   ├── package.json         # Vue3 + TS + Tailwind + Vite
│   └── tailwind.config.ts
├── build/                   # Wails 平台构建配置
├── Taskfile.yml             # Wails 任务入口
├── go.mod
└── go.sum
```

## 已实现核心功能

- 文件导入与预览：按钮选择或拖拽图片，中央 Canvas 大尺寸预览。
- EXIF 展示：Go 后端解析拍摄时间、相机品牌/型号、镜头、GPS 坐标、快门、光圈、ISO 等常用字段。
- 自定义水印：支持自动使用 EXIF 生成水印文本，支持颜色、阴影、字号调整。
- 拖拽定位：用户可在 Canvas 预览区直接拖动水印，位置按相纸归一化保存。
- 照片旋转：支持每张照片独立左转、右转与复位，预览、批量输出和打印保持一致。
- 相纸适配：支持 6 寸、7 寸、A4、方形，包含留白适配与裁切填满两种模式。
- 非破坏性输出：合成图写入系统临时目录 `photomark2`，不会覆盖原始照片。
- 系统打印：点击确认打印后，前端导出最终 PNG，Go 保存临时文件并调用当前系统打印能力。

## 开发命令

```bash
# 安装前端依赖
cd frontend && npm install

# 生成 Go 服务的 TypeScript 绑定
wails3 generate bindings -ts

# 类型检查与前端构建
cd frontend && npm run typecheck && npm run build

# 后端编译检查
go test .

# 开发模式
wails3 dev -config ./build/config.yml -port 9245
```

## 后续开发建议

1. 将 `frontend/src/App.vue` 拆分为 `CanvasPreview.vue`、`ExifPanel.vue`、`WatermarkPanel.vue`、`PrintPanel.vue`。
2. 用 Wails 原生文件拖放事件获取更稳定的绝对路径，避免部分 WebView 只暴露浏览器 `File` 对象。
3. 完善打印设置：目标打印机、份数、横竖版、DPI、无边距、实际纸张尺寸校准。
4. 增加水印模板系统：多行布局、Logo/二维码、透明度、字体、描边、对齐方式。
5. 增加裁切框交互：拖动照片位置、缩放、旋转、按 EXIF Orientation 自动校正。
6. 增加 EXIF 解析覆盖面：MakerNote、时区、反向地理编码、HEIC/AVIF 支持。
7. 增加端到端验证：Canvas 像素快照、导出尺寸测试、平台打印命令模拟测试。
