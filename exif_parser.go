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
	addString(result, "相机品牌", asciiValue(entries[0x010f]))
	addString(result, "相机型号", asciiValue(entries[0x0110]))
	addString(result, "软件", asciiValue(entries[0x0131]))
	addString(result, "修改时间", formatExifTime(asciiValue(entries[0x0132])))

	if value, ok := shortOrLong(entries[0x0100], r); ok {
		result["原图宽度"] = strconv.Itoa(int(value)) + " px"
	}
	if value, ok := shortOrLong(entries[0x0101], r); ok {
		result["原图高度"] = strconv.Itoa(int(value)) + " px"
	}
	if orientation, ok := shortOrLong(entries[0x0112], r); ok {
		result["方向"] = orientationLabel(int(orientation))
	}
}

func (r *tiffReader) collectExifIFD(entries map[uint16]ifdEntry, result map[string]string) {
	addString(result, "拍摄时间", formatExifTime(asciiValue(entries[0x9003])))
	addString(result, "数字化时间", formatExifTime(asciiValue(entries[0x9004])))
	addString(result, "镜头品牌", asciiValue(entries[0xa433]))
	addString(result, "镜头型号", asciiValue(entries[0xa434]))

	if exposure, ok := rationalString(entries[0x829a], r); ok {
		result["快门速度"] = exposure
	}
	if aperture, ok := rationalFloat(entries[0x829d], r); ok {
		result["光圈"] = "f/" + trimFloat(aperture)
	}
	if iso, ok := shortOrLong(entries[0x8827], r); ok {
		result["ISO"] = strconv.Itoa(int(iso))
	}
	if focalLength, ok := rationalFloat(entries[0x920a], r); ok {
		result["焦距"] = trimFloat(focalLength) + " mm"
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
}

func (r *tiffReader) collectGPSIFD(entries map[uint16]ifdEntry, result map[string]string) {
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

func (r *tiffReader) u16(offset int) uint16 {
	return r.order.Uint16(r.data[offset : offset+2])
}

func (r *tiffReader) u32(offset int) uint32 {
	return r.order.Uint32(r.data[offset : offset+4])
}
