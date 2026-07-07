package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type PhotoService struct{}

type ImageInfo struct {
	Path     string            `json:"path"`
	Name     string            `json:"name"`
	Size     int64             `json:"size"`
	MimeType string            `json:"mimeType"`
	Width    int               `json:"width"`
	Height   int               `json:"height"`
	EXIF     map[string]string `json:"exif"`
}

type SavedImage struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type LoadedImage struct {
	Info    *ImageInfo `json:"info"`
	DataURL string     `json:"dataURL"`
}

type AmapAddress struct {
	FormattedAddress string `json:"formattedAddress"`
	Province         string `json:"province"`
	City             string `json:"city"`
	District         string `json:"district"`
	Township         string `json:"township"`
	Street           string `json:"street"`
	Number           string `json:"number"`
	Adcode           string `json:"adcode"`
	Citycode         string `json:"citycode"`
	Location         string `json:"location"`
}

type amapRegeoResponse struct {
	Status    string `json:"status"`
	Info      string `json:"info"`
	Infocode  string `json:"infocode"`
	Regeocode struct {
		FormattedAddress string `json:"formatted_address"`
		AddressComponent struct {
			Province     json.RawMessage `json:"province"`
			City         json.RawMessage `json:"city"`
			District     json.RawMessage `json:"district"`
			Township     json.RawMessage `json:"township"`
			Adcode       json.RawMessage `json:"adcode"`
			Citycode     json.RawMessage `json:"citycode"`
			StreetNumber struct {
				Street json.RawMessage `json:"street"`
				Number json.RawMessage `json:"number"`
			} `json:"streetNumber"`
		} `json:"addressComponent"`
	} `json:"regeocode"`
}

type amapCoordinateConvertResponse struct {
	Status    string `json:"status"`
	Info      string `json:"info"`
	Infocode  string `json:"infocode"`
	Locations string `json:"locations"`
}

func (s *PhotoService) ServiceName() string {
	return "PhotoService"
}

func (s *PhotoService) ReadImage(path string) (*ImageInfo, error) {
	cleanPath, err := validateImagePath(path)
	if err != nil {
		return nil, err
	}

	stat, err := os.Stat(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("读取文件信息失败: %w", err)
	}
	if stat.IsDir() {
		return nil, errors.New("请选择图片文件，而不是文件夹")
	}

	imageConfig, imageFormat, err := imageMetadata(cleanPath)
	if err != nil {
		return nil, err
	}
	width := imageConfig.Width
	height := imageConfig.Height

	exif, err := ParseEXIF(cleanPath)
	if err != nil {
		exif = map[string]string{"解析状态": err.Error()}
	}
	addFileMetadata(exif, cleanPath, stat, imageConfig, imageFormat)

	return &ImageInfo{
		Path:     cleanPath,
		Name:     filepath.Base(cleanPath),
		Size:     stat.Size(),
		MimeType: imageMimeType(cleanPath),
		Width:    width,
		Height:   height,
		EXIF:     exif,
	}, nil
}

func (s *PhotoService) LoadImage(path string) (*LoadedImage, error) {
	info, err := s.ReadImage(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(info.Path)
	if err != nil {
		return nil, fmt.Errorf("读取图片内容失败: %w", err)
	}

	return &LoadedImage{
		Info:    info,
		DataURL: "data:" + info.MimeType + ";base64," + base64.StdEncoding.EncodeToString(data),
	}, nil
}

func (s *PhotoService) ReverseGeocodeAmap(latitude float64, longitude float64, key string) (*AmapAddress, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("请先填写高德 Web 服务 API Key")
	}
	if latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return nil, errors.New("GPS 坐标超出有效范围")
	}

	convertedLongitude, convertedLatitude, err := convertGPSCoordinateAmap(latitude, longitude, key)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("key", key)
	params.Set("location", fmt.Sprintf("%.6f,%.6f", convertedLongitude, convertedLatitude))
	params.Set("extensions", "base")
	params.Set("radius", "1000")
	params.Set("roadlevel", "0")
	params.Set("output", "json")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	endpoint := "https://restapi.amap.com/v3/geocode/regeo?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建高德请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "PhotoMark2/0.0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求高德地址失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("高德服务返回异常状态: %s", resp.Status)
	}

	var decoded amapRegeoResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("解析高德地址失败: %w", err)
	}
	if decoded.Status != "1" {
		if decoded.Info == "" {
			decoded.Info = "未知错误"
		}
		return nil, fmt.Errorf("高德地址解析失败: %s (%s)", decoded.Info, decoded.Infocode)
	}
	component := decoded.Regeocode.AddressComponent
	formattedAddress := strings.TrimSpace(decoded.Regeocode.FormattedAddress)
	province := amapStringField(component.Province)
	city := amapStringField(component.City)
	district := amapStringField(component.District)
	township := amapStringField(component.Township)
	street := amapStringField(component.StreetNumber.Street)
	number := amapStringField(component.StreetNumber.Number)
	adcode := amapStringField(component.Adcode)
	citycode := amapStringField(component.Citycode)
	roughAddress := amapRoughAddress(formattedAddress, province, city, district, township, street)
	if roughAddress == "" {
		return nil, errors.New("高德未返回可用地址")
	}
	return &AmapAddress{
		FormattedAddress: roughAddress,
		Province:         province,
		City:             city,
		District:         district,
		Township:         township,
		Street:           street,
		Number:           number,
		Adcode:           adcode,
		Citycode:         citycode,
		Location:         fmt.Sprintf("%.6f, %.6f", convertedLatitude, convertedLongitude),
	}, nil
}

func convertGPSCoordinateAmap(latitude float64, longitude float64, key string) (float64, float64, error) {
	params := url.Values{}
	params.Set("key", key)
	params.Set("locations", fmt.Sprintf("%.6f,%.6f", longitude, latitude))
	params.Set("coordsys", "gps")
	params.Set("output", "json")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	endpoint := "https://restapi.amap.com/v3/assistant/coordinate/convert?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("创建高德坐标转换请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "PhotoMark2/0.0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("请求高德坐标转换失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("高德坐标转换服务返回异常状态: %s", resp.Status)
	}

	var decoded amapCoordinateConvertResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return 0, 0, fmt.Errorf("解析高德坐标转换失败: %w", err)
	}
	if decoded.Status != "1" {
		if decoded.Info == "" {
			decoded.Info = "未知错误"
		}
		return 0, 0, fmt.Errorf("高德坐标转换失败: %s (%s)", decoded.Info, decoded.Infocode)
	}

	converted := strings.TrimSpace(strings.Split(decoded.Locations, ";")[0])
	parts := strings.Split(converted, ",")
	if len(parts) != 2 {
		return 0, 0, errors.New("高德坐标转换未返回可用坐标")
	}
	convertedLongitude, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("解析高德经度失败: %w", err)
	}
	convertedLatitude, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return 0, 0, fmt.Errorf("解析高德纬度失败: %w", err)
	}
	return convertedLongitude, convertedLatitude, nil
}

func (s *PhotoService) SaveRenderedImage(dataURL string) (*SavedImage, error) {
	mediaType, payload, err := splitDataURL(dataURL)
	if err != nil {
		return nil, err
	}

	ext := ".png"
	if extensions, err := mime.ExtensionsByType(mediaType); err == nil && len(extensions) > 0 {
		ext = extensions[0]
	}
	if !isSupportedOutputExtension(ext) {
		ext = ".png"
	}

	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("解码合成图片失败: %w", err)
	}

	tmpDir := filepath.Join(os.TempDir(), "photomark2")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}

	name := "photomark-" + time.Now().Format("20060102-150405.000") + ext
	outputPath := filepath.Join(tmpDir, name)
	if err := os.WriteFile(outputPath, decoded, 0o644); err != nil {
		return nil, fmt.Errorf("保存合成图片失败: %w", err)
	}

	stat, err := os.Stat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("读取输出文件失败: %w", err)
	}

	return &SavedImage{
		Path: outputPath,
		Name: name,
		Size: stat.Size(),
	}, nil
}

func amapStringField(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return strings.Join(list, "")
	}
	return ""
}

func amapRoughAddress(formattedAddress string, province string, city string, district string, township string, street string) string {
	formattedAddress = strings.TrimSpace(formattedAddress)
	province = strings.TrimSpace(province)
	city = strings.TrimSpace(city)
	district = strings.TrimSpace(district)
	township = strings.TrimSpace(township)
	street = strings.TrimSpace(street)
	if province == "" {
		return firstNonEmpty(joinAddressParts(city, district), joinAddressParts(district, township), city, district, township, street, formattedAddress)
	}
	major := city
	if major == "" || major == province {
		major = firstNonEmpty(district, township, street)
	}
	return firstNonEmpty(joinAddressParts(province, major), province, formattedAddress)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func joinAddressParts(values ...string) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "·")
}

func (s *PhotoService) PrintRenderedImage(dataURL string, orientation string) (*SavedImage, error) {
	saved, err := s.SaveRenderedImage(dataURL)
	if err != nil {
		return nil, err
	}
	if err := PrintImage(saved.Path, orientation); err != nil {
		return saved, err
	}
	return saved, nil
}

func validateImagePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("图片路径为空")
	}

	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析图片路径失败: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(cleanPath))
	if !isSupportedInputExtension(ext) {
		return "", fmt.Errorf("暂不支持 %s 文件，请选择 JPG、PNG、TIFF、BMP 或 WebP 图片", ext)
	}
	return cleanPath, nil
}

func isSupportedInputExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".tif", ".tiff", ".bmp", ".webp":
		return true
	default:
		return false
	}
}

func isSupportedOutputExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".tif", ".tiff":
		return true
	default:
		return false
	}
}

func splitDataURL(dataURL string) (string, string, error) {
	const base64Marker = ";base64,"
	if !strings.HasPrefix(dataURL, "data:") || !strings.Contains(dataURL, base64Marker) {
		return "", "", errors.New("合成图片数据格式无效")
	}

	parts := strings.SplitN(strings.TrimPrefix(dataURL, "data:"), base64Marker, 2)
	if len(parts) != 2 || parts[1] == "" {
		return "", "", errors.New("合成图片数据为空")
	}
	return parts[0], parts[1], nil
}

func imageMimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".bmp":
		return "image/bmp"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func imageMetadata(path string) (image.Config, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return image.Config{}, "", fmt.Errorf("打开图片失败: %w", err)
	}
	defer file.Close()

	cfg, format, err := decodeImageConfig(file)
	if err != nil {
		return image.Config{}, "", fmt.Errorf("读取图片尺寸失败: %w", err)
	}
	return cfg, format, nil
}

func addFileMetadata(exif map[string]string, path string, stat os.FileInfo, cfg image.Config, format string) {
	if exif == nil {
		return
	}
	addString(exif, "文件名", filepath.Base(path))
	addString(exif, "文件扩展名", strings.ToLower(filepath.Ext(path)))
	addString(exif, "文件格式", format)
	addString(exif, "MIME 类型", imageMimeType(path))
	exif["文件大小"] = formatBytes(stat.Size())
	exif["系统修改时间"] = stat.ModTime().Format("2006-01-02 15:04:05")
	if cfg.Width > 0 && cfg.Height > 0 {
		exif["图片尺寸"] = fmt.Sprintf("%d x %d px", cfg.Width, cfg.Height)
		exif["像素总数"] = formatMegapixels(cfg.Width, cfg.Height)
		exif["宽高比"] = aspectRatioLabel(cfg.Width, cfg.Height)
	}
	addString(exif, "颜色模型", colorModelLabel(cfg.ColorModel))
}

func formatBytes(size int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(size)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", size, units[unit])
	}
	return fmt.Sprintf("%.2f %s", value, units[unit])
}

func formatMegapixels(width int, height int) string {
	pixels := float64(width*height) / 1000000
	return fmt.Sprintf("%.2f MP", pixels)
}

func aspectRatioLabel(width int, height int) string {
	divisor := gcd(width, height)
	if divisor <= 0 {
		return ""
	}
	return fmt.Sprintf("%d:%d", width/divisor, height/divisor)
}

func gcd(a int, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func colorModelLabel(model color.Model) string {
	switch model {
	case color.RGBAModel:
		return "RGBA"
	case color.RGBA64Model:
		return "RGBA64"
	case color.NRGBAModel:
		return "NRGBA"
	case color.NRGBA64Model:
		return "NRGBA64"
	case color.AlphaModel:
		return "Alpha"
	case color.Alpha16Model:
		return "Alpha16"
	case color.GrayModel:
		return "Gray"
	case color.Gray16Model:
		return "Gray16"
	case color.CMYKModel:
		return "CMYK"
	case color.YCbCrModel:
		return "YCbCr"
	default:
		if model == nil {
			return ""
		}
		return fmt.Sprintf("%T", model)
	}
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
