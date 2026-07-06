package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
			Province     string          `json:"province"`
			City         json.RawMessage `json:"city"`
			District     string          `json:"district"`
			Township     string          `json:"township"`
			Adcode       string          `json:"adcode"`
			Citycode     string          `json:"citycode"`
			StreetNumber struct {
				Street string `json:"street"`
				Number string `json:"number"`
			} `json:"streetNumber"`
		} `json:"addressComponent"`
	} `json:"regeocode"`
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

	width, height, err := imageDimensions(cleanPath)
	if err != nil {
		return nil, err
	}

	exif, err := ParseEXIF(cleanPath)
	if err != nil {
		exif = map[string]string{"解析状态": err.Error()}
	}

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

	params := url.Values{}
	params.Set("key", key)
	params.Set("location", fmt.Sprintf("%.6f,%.6f", longitude, latitude))
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
	if strings.TrimSpace(decoded.Regeocode.FormattedAddress) == "" {
		return nil, errors.New("高德未返回可用地址")
	}

	component := decoded.Regeocode.AddressComponent
	return &AmapAddress{
		FormattedAddress: decoded.Regeocode.FormattedAddress,
		Province:         component.Province,
		City:             amapStringField(component.City),
		District:         component.District,
		Township:         component.Township,
		Street:           component.StreetNumber.Street,
		Number:           component.StreetNumber.Number,
		Adcode:           component.Adcode,
		Citycode:         component.Citycode,
		Location:         fmt.Sprintf("%.6f, %.6f", latitude, longitude),
	}, nil
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

func imageDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("打开图片失败: %w", err)
	}
	defer file.Close()

	cfg, _, err := decodeImageConfig(file)
	if err != nil {
		return 0, 0, fmt.Errorf("读取图片尺寸失败: %w", err)
	}
	return cfg.Width, cfg.Height, nil
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
