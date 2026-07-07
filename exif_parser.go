package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const exifHeader = "Exif\x00\x00"

type tiffReader struct {
	data  []byte
	order binary.ByteOrder
}

type ifdEntry struct {
	tag        uint16
	fieldType  uint16
	count      uint32
	valueBytes []byte
}

func ParseEXIF(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 EXIF 数据失败: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	var tiff []byte
	switch {
	case len(raw) >= 4 && raw[0] == 0xff && raw[1] == 0xd8:
		tiff, err = jpegExifPayload(raw)
	case ext == ".tif" || ext == ".tiff":
		tiff = raw
	default:
		return map[string]string{"解析状态": "该格式通常不包含标准 EXIF，或当前版本暂未解析"}, nil
	}
	if err != nil {
		return map[string]string{"解析状态": err.Error()}, nil
	}

	result, err := parseTIFFEXIF(tiff)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		result["解析状态"] = "未找到可展示的 EXIF 元数据"
	}
	return result, nil
}

func jpegExifPayload(data []byte) ([]byte, error) {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return nil, errors.New("不是有效的 JPEG 文件")
	}

	offset := 2
	for offset+4 <= len(data) {
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			break
		}

		marker := data[offset]
		offset++
		if marker == 0xd9 || marker == 0xda {
			break
		}
		if offset+2 > len(data) {
			break
		}

		segmentLength := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		offset += 2
		if segmentLength < 2 || offset+segmentLength-2 > len(data) {
			break
		}

		segment := data[offset : offset+segmentLength-2]
		if marker == 0xe1 && len(segment) > len(exifHeader) && string(segment[:len(exifHeader)]) == exifHeader {
			return segment[len(exifHeader):], nil
		}
		offset += segmentLength - 2
	}

	return nil, errors.New("未找到 EXIF APP1 数据")
}

func parseTIFFEXIF(data []byte) (map[string]string, error) {
	if len(data) < 8 {
		return nil, errors.New("EXIF/TIFF 数据过短")
	}

	reader := &tiffReader{data: data}
	switch string(data[:2]) {
	case "II":
		reader.order = binary.LittleEndian
	case "MM":
		reader.order = binary.BigEndian
	default:
		return nil, errors.New("EXIF 字节序无效")
	}
	if reader.u16(2) != 42 {
		return nil, errors.New("TIFF 标识无效")
	}

	result := map[string]string{}
	ifd0Offset := reader.u32(4)
	ifd0, err := reader.readIFD(ifd0Offset)
	if err != nil {
		return nil, err
	}
	reader.collectIFD0(ifd0, result)

	if exifOffset, ok := entryLong(ifd0, 0x8769, reader); ok {
		if exifIFD, err := reader.readIFD(exifOffset); err == nil {
			reader.collectExifIFD(exifIFD, result)
		}
	}
	if gpsOffset, ok := entryLong(ifd0, 0x8825, reader); ok {
		if gpsIFD, err := reader.readIFD(gpsOffset); err == nil {
			reader.collectGPSIFD(gpsIFD, result)
		}
	}

	return result, nil
}

func (r *tiffReader) readIFD(offset uint32) (map[uint16]ifdEntry, error) {
	start := int(offset)
	if start < 0 || start+2 > len(r.data) {
		return nil, fmt.Errorf("IFD 偏移越界: %d", offset)
	}

	count := int(r.u16(start))
	cursor := start + 2
	entries := map[uint16]ifdEntry{}
	for i := 0; i < count; i++ {
		if cursor+12 > len(r.data) {
			return nil, errors.New("IFD 条目数据不完整")
		}

		tag := r.u16(cursor)
		fieldType := r.u16(cursor + 2)
		itemCount := r.u32(cursor + 4)
		valueOrOffset := r.data[cursor+8 : cursor+12]
		valueBytes := r.valueBytes(fieldType, itemCount, valueOrOffset)
		if valueBytes != nil {
			entries[tag] = ifdEntry{
				tag:        tag,
				fieldType:  fieldType,
				count:      itemCount,
				valueBytes: valueBytes,
			}
		}
		cursor += 12
	}
	return entries, nil
}

func (r *tiffReader) collectIFD0(entries map[uint16]ifdEntry, result map[string]string) {
	addString(result, "图片描述", asciiValue(entries[0x010e]))
	addString(result, "相机品牌", asciiValue(entries[0x010f]))
	addString(result, "相机型号", asciiValue(entries[0x0110]))
	addString(result, "软件", asciiValue(entries[0x0131]))
	addString(result, "修改时间", formatExifTime(asciiValue(entries[0x0132])))
	addString(result, "作者", asciiValue(entries[0x013b]))
	addString(result, "主机软件", asciiValue(entries[0x013c]))
	addString(result, "版权", asciiValue(entries[0x8298]))

	if value, ok := shortOrLong(entries[0x0100], r); ok {
		result["原图宽度"] = strconv.Itoa(int(value)) + " px"
	}
	if value, ok := shortOrLong(entries[0x0101], r); ok {
		result["原图高度"] = strconv.Itoa(int(value)) + " px"
	}
	if orientation, ok := shortOrLong(entries[0x0112], r); ok {
		result["方向"] = orientationLabel(int(orientation))
	}
	if xres, ok := rationalFloat(entries[0x011a], r); ok {
		result["水平分辨率"] = trimFloat(xres)
	}
	if yres, ok := rationalFloat(entries[0x011b], r); ok {
		result["垂直分辨率"] = trimFloat(yres)
	}
	if unit, ok := shortOrLong(entries[0x0128], r); ok {
		result["分辨率单位"] = resolutionUnitLabel(int(unit))
	}
}

func (r *tiffReader) collectExifIFD(entries map[uint16]ifdEntry, result map[string]string) {
	addString(result, "拍摄时间", formatExifTime(asciiValue(entries[0x9003])))
	addString(result, "数字化时间", formatExifTime(asciiValue(entries[0x9004])))
	addString(result, "拍摄时间小数秒", asciiValue(entries[0x9291]))
	addString(result, "数字化时间小数秒", asciiValue(entries[0x9292]))
	addString(result, "时区", asciiValue(entries[0x9010]))
	addString(result, "拍摄时区", asciiValue(entries[0x9011]))
	addString(result, "数字化时区", asciiValue(entries[0x9012]))
	addString(result, "相机所有者", asciiValue(entries[0xa430]))
	addString(result, "机身序列号", asciiValue(entries[0xa431]))
	addString(result, "镜头品牌", asciiValue(entries[0xa433]))
	addString(result, "镜头型号", asciiValue(entries[0xa434]))
	addString(result, "镜头序列号", asciiValue(entries[0xa435]))
	addString(result, "用户注释", userCommentValue(entries[0x9286]))
	addString(result, "EXIF 版本", versionValue(entries[0x9000]))
	addString(result, "FlashPix 版本", versionValue(entries[0xa000]))

	if exposure, ok := rationalString(entries[0x829a], r); ok {
		result["快门速度"] = exposure
	}
	if aperture, ok := rationalFloat(entries[0x829d], r); ok {
		result["光圈"] = "f/" + trimFloat(aperture)
	}
	if program, ok := shortOrLong(entries[0x8822], r); ok {
		result["曝光程序"] = exposureProgramLabel(int(program))
	}
	if iso, ok := shortOrLong(entries[0x8827], r); ok {
		result["ISO"] = strconv.Itoa(int(iso))
	}
	if sensitivity, ok := shortOrLong(entries[0x8830], r); ok {
		result["感光度类型"] = sensitivityTypeLabel(int(sensitivity))
	}
	if recommendedExposureIndex, ok := shortOrLong(entries[0x8832], r); ok {
		result["推荐曝光指数"] = strconv.Itoa(int(recommendedExposureIndex))
	}
	if shutter, ok := signedRationalFloat(entries[0x9201], r); ok {
		result["快门速度值"] = trimFloat(shutter) + " EV"
	}
	if apertureValue, ok := rationalFloat(entries[0x9202], r); ok {
		result["光圈值"] = trimFloat(apertureValue) + " EV"
	}
	if brightness, ok := signedRationalFloat(entries[0x9203], r); ok {
		result["亮度值"] = trimFloat(brightness) + " EV"
	}
	if exposureBias, ok := signedRationalFloat(entries[0x9204], r); ok {
		result["曝光补偿"] = trimFloat(exposureBias) + " EV"
	}
	if maxAperture, ok := rationalFloat(entries[0x9205], r); ok {
		result["最大光圈值"] = trimFloat(maxAperture) + " EV"
	}
	if distance, ok := rationalFloat(entries[0x9206], r); ok {
		result["主体距离"] = trimFloat(distance) + " m"
	}
	if mode, ok := shortOrLong(entries[0x9207], r); ok {
		result["测光模式"] = meteringModeLabel(int(mode))
	}
	if source, ok := shortOrLong(entries[0x9208], r); ok {
		result["光源"] = lightSourceLabel(int(source))
	}
	if flash, ok := shortOrLong(entries[0x9209], r); ok {
		result["闪光灯"] = flashLabel(int(flash))
	}
	if focalLength, ok := rationalFloat(entries[0x920a], r); ok {
		result["焦距"] = trimFloat(focalLength) + " mm"
	}
	if colorSpace, ok := shortOrLong(entries[0xa001], r); ok {
		result["色彩空间"] = colorSpaceLabel(int(colorSpace))
	}
	if focal35, ok := shortOrLong(entries[0xa405], r); ok {
		result["等效焦距"] = strconv.Itoa(int(focal35)) + " mm"
	}
	if width, ok := shortOrLong(entries[0xa002], r); ok {
		result["EXIF 宽度"] = strconv.Itoa(int(width)) + " px"
	}
	if height, ok := shortOrLong(entries[0xa003], r); ok {
		result["EXIF 高度"] = strconv.Itoa(int(height)) + " px"
	}
	if sensing, ok := shortOrLong(entries[0xa217], r); ok {
		result["传感器类型"] = sensingMethodLabel(int(sensing))
	}
	if source, ok := byteValue(entries[0xa300]); ok {
		result["文件来源"] = fileSourceLabel(int(source))
	}
	if scene, ok := byteValue(entries[0xa301]); ok {
		result["场景类型"] = sceneTypeLabel(int(scene))
	}
	if rendered, ok := shortOrLong(entries[0xa401], r); ok {
		result["自定义渲染"] = customRenderedLabel(int(rendered))
	}
	if mode, ok := shortOrLong(entries[0xa402], r); ok {
		result["曝光模式"] = exposureModeLabel(int(mode))
	}
	if balance, ok := shortOrLong(entries[0xa403], r); ok {
		result["白平衡"] = whiteBalanceLabel(int(balance))
	}
	if zoom, ok := rationalFloat(entries[0xa404], r); ok {
		result["数字变焦"] = trimFloat(zoom) + "x"
	}
	if scene, ok := shortOrLong(entries[0xa406], r); ok {
		result["拍摄场景"] = sceneCaptureTypeLabel(int(scene))
	}
	if gain, ok := shortOrLong(entries[0xa407], r); ok {
		result["增益控制"] = gainControlLabel(int(gain))
	}
	if contrast, ok := shortOrLong(entries[0xa408], r); ok {
		result["对比度"] = contrastLabel(int(contrast))
	}
	if saturation, ok := shortOrLong(entries[0xa409], r); ok {
		result["饱和度"] = saturationLabel(int(saturation))
	}
	if sharpness, ok := shortOrLong(entries[0xa40a], r); ok {
		result["锐度"] = sharpnessLabel(int(sharpness))
	}
	if distanceRange, ok := shortOrLong(entries[0xa40c], r); ok {
		result["主体距离范围"] = subjectDistanceRangeLabel(int(distanceRange))
	}
	if lensSpec, ok := rationalList(entries[0xa432], r); ok {
		result["镜头规格"] = lensSpec
	}
}

func (r *tiffReader) collectGPSIFD(entries map[uint16]ifdEntry, result map[string]string) {
	if version, ok := gpsVersion(entries[0x0000]); ok {
		result["GPS 版本"] = version
	}
	lat, latOK := gpsCoordinate(entries[0x0002], asciiValue(entries[0x0001]), r)
	lon, lonOK := gpsCoordinate(entries[0x0004], asciiValue(entries[0x0003]), r)
	if latOK && lonOK {
		result["GPS 坐标"] = fmt.Sprintf("%.6f, %.6f", lat, lon)
		result["GPS 地点"] = fmt.Sprintf("https://maps.google.com/?q=%.6f,%.6f", lat, lon)
	}
	if altitude, ok := rationalFloat(entries[0x0006], r); ok {
		if ref, refOK := shortOrLong(entries[0x0005], r); refOK && ref == 1 {
			altitude = -altitude
		}
		result["GPS 海拔"] = trimFloat(altitude) + " m"
	}
	if dop, ok := rationalFloat(entries[0x000b], r); ok {
		result["GPS 精度因子"] = trimFloat(dop)
	}
	if speed, ok := rationalFloat(entries[0x000d], r); ok {
		unit := gpsSpeedUnit(asciiValue(entries[0x000c]))
		result["GPS 速度"] = trimFloat(speed) + unit
	}
	if track, ok := rationalFloat(entries[0x000f], r); ok {
		result["GPS 移动方向"] = trimFloat(track) + "° " + gpsDirectionRef(asciiValue(entries[0x000e]))
	}
	if direction, ok := rationalFloat(entries[0x0011], r); ok {
		result["GPS 拍摄方向"] = trimFloat(direction) + "° " + gpsDirectionRef(asciiValue(entries[0x0010]))
	}
	addString(result, "GPS 地图基准", asciiValue(entries[0x0012]))
	addString(result, "GPS 处理方式", gpsTextValue(entries[0x001b]))
	addString(result, "GPS 区域信息", gpsTextValue(entries[0x001c]))
	addString(result, "GPS 日期", asciiValue(entries[0x001d]))
	if stamp, ok := gpsTimestamp(entries[0x0007], r); ok {
		result["GPS 时间"] = stamp
	}
}

func (r *tiffReader) valueBytes(fieldType uint16, count uint32, inline []byte) []byte {
	unitSize := fieldTypeSize(fieldType)
	if unitSize == 0 || count > math.MaxUint32/uint32(unitSize) {
		return nil
	}

	byteCount := int(count) * unitSize
	if byteCount <= 4 {
		value := make([]byte, byteCount)
		copy(value, inline[:byteCount])
		return value
	}

	offset := int(r.order.Uint32(inline))
	if offset < 0 || offset+byteCount > len(r.data) {
		return nil
	}
	return r.data[offset : offset+byteCount]
}

func fieldTypeSize(fieldType uint16) int {
	switch fieldType {
	case 1, 2, 7:
		return 1
	case 3:
		return 2
	case 4, 9:
		return 4
	case 5, 10:
		return 8
	default:
		return 0
	}
}

func asciiValue(entry ifdEntry) string {
	if entry.valueBytes == nil || entry.fieldType != 2 {
		return ""
	}
	value := strings.TrimRight(string(entry.valueBytes), "\x00 ")
	return strings.TrimSpace(value)
}

func addString(result map[string]string, label, value string) {
	if strings.TrimSpace(value) != "" {
		result[label] = value
	}
}

func formatExifTime(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	if parsed, err := timeParseEXIF(value); err == nil {
		return parsed
	}
	return value
}

func timeParseEXIF(value string) (string, error) {
	if len(value) < len("2006:01:02 15:04:05") {
		return "", errors.New("invalid EXIF time")
	}
	t, err := time.Parse("2006:01:02 15:04:05", value[:19])
	if err != nil {
		return "", err
	}
	return t.Format("2006-01-02 15:04:05"), nil
}

func entryLong(entries map[uint16]ifdEntry, tag uint16, r *tiffReader) (uint32, bool) {
	entry, ok := entries[tag]
	if !ok {
		return 0, false
	}
	return shortOrLong(entry, r)
}

func shortOrLong(entry ifdEntry, r *tiffReader) (uint32, bool) {
	if len(entry.valueBytes) == 0 || entry.count == 0 {
		return 0, false
	}

	switch entry.fieldType {
	case 3:
		if len(entry.valueBytes) < 2 {
			return 0, false
		}
		return uint32(r.order.Uint16(entry.valueBytes[:2])), true
	case 4:
		if len(entry.valueBytes) < 4 {
			return 0, false
		}
		return r.order.Uint32(entry.valueBytes[:4]), true
	default:
		return 0, false
	}
}

func byteValue(entry ifdEntry) (byte, bool) {
	if len(entry.valueBytes) == 0 || entry.fieldType != 7 {
		return 0, false
	}
	return entry.valueBytes[0], true
}

func rationalString(entry ifdEntry, r *tiffReader) (string, bool) {
	if len(entry.valueBytes) < 8 || entry.fieldType != 5 {
		return "", false
	}
	num := r.order.Uint32(entry.valueBytes[0:4])
	den := r.order.Uint32(entry.valueBytes[4:8])
	if den == 0 {
		return "", false
	}
	if num < den {
		return fmt.Sprintf("%d/%d s", num, den), true
	}
	return trimFloat(float64(num)/float64(den)) + " s", true
}

func rationalFloat(entry ifdEntry, r *tiffReader) (float64, bool) {
	if len(entry.valueBytes) < 8 || entry.fieldType != 5 {
		return 0, false
	}
	num := r.order.Uint32(entry.valueBytes[0:4])
	den := r.order.Uint32(entry.valueBytes[4:8])
	if den == 0 {
		return 0, false
	}
	return float64(num) / float64(den), true
}

func signedRationalFloat(entry ifdEntry, r *tiffReader) (float64, bool) {
	if len(entry.valueBytes) < 8 || entry.fieldType != 10 {
		return 0, false
	}
	num := int32(r.order.Uint32(entry.valueBytes[0:4]))
	den := int32(r.order.Uint32(entry.valueBytes[4:8]))
	if den == 0 {
		return 0, false
	}
	return float64(num) / float64(den), true
}

func rationalList(entry ifdEntry, r *tiffReader) (string, bool) {
	if entry.fieldType != 5 || entry.count == 0 || len(entry.valueBytes) < int(entry.count)*8 {
		return "", false
	}
	values := make([]string, 0, entry.count)
	for i := 0; i < int(entry.count); i++ {
		value, ok := rationalAt(entry.valueBytes, i*8, r)
		if !ok {
			continue
		}
		if value > 0 {
			values = append(values, trimFloat(value))
		}
	}
	if len(values) == 0 {
		return "", false
	}
	if len(values) == 4 {
		return fmt.Sprintf("%s-%s mm f/%s-%s", values[0], values[1], values[2], values[3]), true
	}
	return strings.Join(values, ", "), true
}

func versionValue(entry ifdEntry) string {
	if len(entry.valueBytes) == 0 {
		return ""
	}
	value := strings.TrimSpace(strings.TrimRight(string(entry.valueBytes), "\x00"))
	if len(value) == 4 && allDigits(value) {
		return value[:2] + "." + value[2:]
	}
	return value
}

func userCommentValue(entry ifdEntry) string {
	if len(entry.valueBytes) == 0 || entry.fieldType != 7 {
		return ""
	}
	data := entry.valueBytes
	if len(data) >= 8 {
		prefix := strings.TrimRight(string(data[:8]), "\x00 ")
		switch prefix {
		case "ASCII", "UNICODE", "JIS":
			data = data[8:]
		}
	}
	return cleanTextBytes(data)
}

func gpsTextValue(entry ifdEntry) string {
	if len(entry.valueBytes) == 0 || entry.fieldType != 7 {
		return ""
	}
	return cleanTextBytes(entry.valueBytes)
}

func cleanTextBytes(data []byte) string {
	value := strings.TrimRight(string(data), "\x00 ")
	value = strings.ReplaceAll(value, "\x00", "")
	return strings.TrimSpace(value)
}

func allDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func gpsVersion(entry ifdEntry) (string, bool) {
	if entry.fieldType != 1 || len(entry.valueBytes) < 4 {
		return "", false
	}
	return fmt.Sprintf("%d.%d.%d.%d", entry.valueBytes[0], entry.valueBytes[1], entry.valueBytes[2], entry.valueBytes[3]), true
}

func gpsCoordinate(entry ifdEntry, ref string, r *tiffReader) (float64, bool) {
	if entry.fieldType != 5 || entry.count < 3 || len(entry.valueBytes) < 24 {
		return 0, false
	}

	degrees, ok := rationalAt(entry.valueBytes, 0, r)
	if !ok {
		return 0, false
	}
	minutes, ok := rationalAt(entry.valueBytes, 8, r)
	if !ok {
		return 0, false
	}
	seconds, ok := rationalAt(entry.valueBytes, 16, r)
	if !ok {
		return 0, false
	}

	value := degrees + minutes/60 + seconds/3600
	if strings.EqualFold(ref, "S") || strings.EqualFold(ref, "W") {
		value = -value
	}
	return value, true
}

func gpsTimestamp(entry ifdEntry, r *tiffReader) (string, bool) {
	if entry.fieldType != 5 || entry.count < 3 || len(entry.valueBytes) < 24 {
		return "", false
	}

	hour, ok := rationalAt(entry.valueBytes, 0, r)
	if !ok {
		return "", false
	}
	minute, ok := rationalAt(entry.valueBytes, 8, r)
	if !ok {
		return "", false
	}
	second, ok := rationalAt(entry.valueBytes, 16, r)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%02d:%02d:%02d UTC", int(hour), int(minute), int(second)), true
}

func rationalAt(data []byte, offset int, r *tiffReader) (float64, bool) {
	if offset+8 > len(data) {
		return 0, false
	}
	num := r.order.Uint32(data[offset : offset+4])
	den := r.order.Uint32(data[offset+4 : offset+8])
	if den == 0 {
		return 0, false
	}
	return float64(num) / float64(den), true
}

func trimFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func orientationLabel(value int) string {
	switch value {
	case 1:
		return "正常"
	case 2:
		return "水平翻转"
	case 3:
		return "旋转 180°"
	case 4:
		return "垂直翻转"
	case 5:
		return "转置"
	case 6:
		return "顺时针旋转 90°"
	case 7:
		return "横向转置"
	case 8:
		return "逆时针旋转 90°"
	default:
		return strconv.Itoa(value)
	}
}

func resolutionUnitLabel(value int) string {
	switch value {
	case 1:
		return "无单位"
	case 2:
		return "英寸"
	case 3:
		return "厘米"
	default:
		return strconv.Itoa(value)
	}
}

func exposureProgramLabel(value int) string {
	switch value {
	case 0:
		return "未定义"
	case 1:
		return "手动"
	case 2:
		return "普通程序"
	case 3:
		return "光圈优先"
	case 4:
		return "快门优先"
	case 5:
		return "创意程序"
	case 6:
		return "动作程序"
	case 7:
		return "人像模式"
	case 8:
		return "风景模式"
	default:
		return strconv.Itoa(value)
	}
}

func sensitivityTypeLabel(value int) string {
	switch value {
	case 0:
		return "未知"
	case 1:
		return "标准输出感光度"
	case 2:
		return "推荐曝光指数"
	case 3:
		return "ISO 感光度"
	case 4:
		return "标准输出感光度和推荐曝光指数"
	case 5:
		return "标准输出感光度和 ISO 感光度"
	case 6:
		return "推荐曝光指数和 ISO 感光度"
	case 7:
		return "标准输出感光度、推荐曝光指数和 ISO 感光度"
	default:
		return strconv.Itoa(value)
	}
}

func meteringModeLabel(value int) string {
	switch value {
	case 0:
		return "未知"
	case 1:
		return "平均测光"
	case 2:
		return "中央重点平均"
	case 3:
		return "点测光"
	case 4:
		return "多点测光"
	case 5:
		return "多区测光"
	case 6:
		return "局部测光"
	case 255:
		return "其他"
	default:
		return strconv.Itoa(value)
	}
}

func lightSourceLabel(value int) string {
	switch value {
	case 0:
		return "未知"
	case 1:
		return "日光"
	case 2:
		return "荧光灯"
	case 3:
		return "钨丝灯"
	case 4:
		return "闪光灯"
	case 9:
		return "晴天"
	case 10:
		return "阴天"
	case 11:
		return "阴影"
	case 12:
		return "日光荧光灯"
	case 13:
		return "日白荧光灯"
	case 14:
		return "冷白荧光灯"
	case 15:
		return "白荧光灯"
	case 17:
		return "标准光 A"
	case 18:
		return "标准光 B"
	case 19:
		return "标准光 C"
	case 20:
		return "D55"
	case 21:
		return "D65"
	case 22:
		return "D75"
	case 23:
		return "D50"
	case 24:
		return "ISO 演播室钨丝灯"
	case 255:
		return "其他"
	default:
		return strconv.Itoa(value)
	}
}

func flashLabel(value int) string {
	if value&1 == 0 {
		return "未闪光"
	}
	parts := []string{"已闪光"}
	if value&0x06 == 0x06 {
		parts = append(parts, "检测到回光")
	} else if value&0x04 == 0x04 {
		parts = append(parts, "未检测到回光")
	}
	if value&0x18 == 0x18 {
		parts = append(parts, "自动模式")
	} else if value&0x08 == 0x08 {
		parts = append(parts, "强制闪光")
	} else if value&0x10 == 0x10 {
		parts = append(parts, "强制不闪光")
	}
	if value&0x40 == 0x40 {
		parts = append(parts, "红眼消除")
	}
	return strings.Join(parts, "，")
}

func colorSpaceLabel(value int) string {
	switch value {
	case 1:
		return "sRGB"
	case 65535:
		return "未校准"
	default:
		return strconv.Itoa(value)
	}
}

func sensingMethodLabel(value int) string {
	switch value {
	case 1:
		return "未定义"
	case 2:
		return "单芯片彩色区域传感器"
	case 3:
		return "双芯片彩色区域传感器"
	case 4:
		return "三芯片彩色区域传感器"
	case 5:
		return "彩色顺序区域传感器"
	case 7:
		return "三线性传感器"
	case 8:
		return "彩色顺序线性传感器"
	default:
		return strconv.Itoa(value)
	}
}

func fileSourceLabel(value int) string {
	if value == 3 {
		return "数码相机"
	}
	return strconv.Itoa(value)
}

func sceneTypeLabel(value int) string {
	if value == 1 {
		return "直接拍摄图像"
	}
	return strconv.Itoa(value)
}

func customRenderedLabel(value int) string {
	switch value {
	case 0:
		return "普通处理"
	case 1:
		return "自定义处理"
	default:
		return strconv.Itoa(value)
	}
}

func exposureModeLabel(value int) string {
	switch value {
	case 0:
		return "自动曝光"
	case 1:
		return "手动曝光"
	case 2:
		return "自动包围曝光"
	default:
		return strconv.Itoa(value)
	}
}

func whiteBalanceLabel(value int) string {
	switch value {
	case 0:
		return "自动"
	case 1:
		return "手动"
	default:
		return strconv.Itoa(value)
	}
}

func sceneCaptureTypeLabel(value int) string {
	switch value {
	case 0:
		return "标准"
	case 1:
		return "风景"
	case 2:
		return "人像"
	case 3:
		return "夜景"
	default:
		return strconv.Itoa(value)
	}
}

func gainControlLabel(value int) string {
	switch value {
	case 0:
		return "无"
	case 1:
		return "低增益提升"
	case 2:
		return "高增益提升"
	case 3:
		return "低增益降低"
	case 4:
		return "高增益降低"
	default:
		return strconv.Itoa(value)
	}
}

func contrastLabel(value int) string {
	switch value {
	case 0:
		return "标准"
	case 1:
		return "柔和"
	case 2:
		return "强烈"
	default:
		return strconv.Itoa(value)
	}
}

func saturationLabel(value int) string {
	switch value {
	case 0:
		return "标准"
	case 1:
		return "低饱和"
	case 2:
		return "高饱和"
	default:
		return strconv.Itoa(value)
	}
}

func sharpnessLabel(value int) string {
	switch value {
	case 0:
		return "标准"
	case 1:
		return "柔和"
	case 2:
		return "锐利"
	default:
		return strconv.Itoa(value)
	}
}

func subjectDistanceRangeLabel(value int) string {
	switch value {
	case 0:
		return "未知"
	case 1:
		return "微距"
	case 2:
		return "近景"
	case 3:
		return "远景"
	default:
		return strconv.Itoa(value)
	}
}

func gpsSpeedUnit(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "K":
		return " km/h"
	case "M":
		return " mph"
	case "N":
		return " kn"
	default:
		return ""
	}
}

func gpsDirectionRef(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "M") {
		return "磁北"
	}
	if strings.EqualFold(strings.TrimSpace(value), "T") {
		return "真北"
	}
	return ""
}

func (r *tiffReader) u16(offset int) uint16 {
	return r.order.Uint16(r.data[offset : offset+2])
}

func (r *tiffReader) u32(offset int) uint32 {
	return r.order.Uint32(r.data[offset : offset+4])
}
