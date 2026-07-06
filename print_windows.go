//go:build windows

package main

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errPrintCancelled = errors.New("print cancelled")

var (
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procPrintDlgW            = comdlg32.NewProc("PrintDlgW")
	procCommDlgExtendedError = comdlg32.NewProc("CommDlgExtendedError")
	procStartDocW            = gdi32.NewProc("StartDocW")
	procEndDoc               = gdi32.NewProc("EndDoc")
	procStartPage            = gdi32.NewProc("StartPage")
	procEndPage              = gdi32.NewProc("EndPage")
	procDeleteDC             = gdi32.NewProc("DeleteDC")
	procGetDeviceCaps        = gdi32.NewProc("GetDeviceCaps")
	procSetStretchBltMode    = gdi32.NewProc("SetStretchBltMode")
	procStretchDIBits        = gdi32.NewProc("StretchDIBits")
	procResetDCW             = gdi32.NewProc("ResetDCW")
	procGlobalLock           = kernel32.NewProc("GlobalLock")
	procGlobalUnlock         = kernel32.NewProc("GlobalUnlock")
	procGlobalFree           = kernel32.NewProc("GlobalFree")
)

type printDlg struct {
	lStructSize         uint32
	hwndOwner           windows.Handle
	hDevMode            windows.Handle
	hDevNames           windows.Handle
	hDC                 windows.Handle
	flags               uint32
	nFromPage           uint16
	nToPage             uint16
	nMinPage            uint16
	nMaxPage            uint16
	nCopies             uint16
	hInstance           windows.Handle
	lCustData           uintptr
	lpfnPrintHook       uintptr
	lpfnSetupHook       uintptr
	lpPrintTemplateName *uint16
	lpSetupTemplateName *uint16
	hPrintTemplate      windows.Handle
	hSetupTemplate      windows.Handle
}

type docInfo struct {
	cbSize       int32
	lpszDocName  *uint16
	lpszOutput   *uint16
	lpszDatatype *uint16
	fwType       uint32
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	bmiHeader bitmapInfoHeader
	bmiColors [3]byte
}

type devMode struct {
	dmDeviceName    [32]uint16
	dmSpecVersion   uint16
	dmDriverVersion uint16
	dmSize          uint16
	dmDriverExtra   uint16
	dmFields        uint32
	dmOrientation   int16
	dmPaperSize     int16
	dmPaperLength   int16
	dmPaperWidth    int16
	dmScale         int16
	dmCopies        int16
	dmDefaultSource int16
	dmPrintQuality  int16
	dmColor         int16
	dmDuplex        int16
	dmYResolution   int16
	dmTTOption      int16
	dmCollate       int16
	dmFormName      [32]uint16
	dmLogPixels     uint16
	dmBitsPerPel    uint32
	dmPelsWidth     uint32
	dmPelsHeight    uint32
}

type printJob struct {
	hdc      windows.Handle
	hDevMode windows.Handle
	hDevName windows.Handle
}

const (
	pdNoSelection                = 0x00000004
	pdNoPageNums                 = 0x00000008
	pdReturnDC                   = 0x00000100
	pdReturnDefault              = 0x00000400
	pdUseDevModeCopiesAndCollate = 0x00040000
	dmOrientationField           = 0x00000001
	dmOrientPortrait             = 1
	dmOrientLandscape            = 2
	horzRes                      = 8
	vertRes                      = 10
	biRGB                        = 0
	dibRGBColors                 = 0
	srcCopy                      = 0x00CC0020
	halftone                     = 4
	spError                      = -1
)

func PrintImage(path string, orientation string) error {
	cleanPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve print file path failed: %w", err)
	}
	if _, err := os.Stat(cleanPath); err != nil {
		return fmt.Errorf("print file is not available: %w", err)
	}

	img, err := loadPrintImage(cleanPath)
	if err != nil {
		return err
	}

	printOrientation := normalizePrintOrientation(orientation, img.Bounds().Dx(), img.Bounds().Dy())
	job, err := showPrintDialog(printOrientation)
	if err != nil {
		return err
	}
	defer job.cleanup()

	if err := printImageToDC(job.hdc, filepath.Base(cleanPath), img, printOrientation); err != nil {
		return err
	}
	return nil
}

func loadPrintImage(path string) (*image.RGBA, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open print image failed: %w", err)
	}
	defer file.Close()

	decoded, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode print image failed: %w", err)
	}

	bounds := decoded.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(rgba, rgba.Bounds(), decoded, bounds.Min, draw.Src)
	return rgba, nil
}

func normalizePrintOrientation(orientation string, imageWidth, imageHeight int) string {
	switch orientation {
	case "portrait", "landscape":
		return orientation
	default:
		if imageWidth < imageHeight {
			return "portrait"
		}
		return "landscape"
	}
}

func showPrintDialog(orientation string) (*printJob, error) {
	defaults, err := defaultPrintSettings(orientation)
	if err != nil {
		defaults = printDlg{}
	}
	pd := printDlg{
		lStructSize: uint32(unsafe.Sizeof(printDlg{})),
		hDevMode:    defaults.hDevMode,
		hDevNames:   defaults.hDevNames,
		flags:       pdReturnDC | pdNoSelection | pdNoPageNums | pdUseDevModeCopiesAndCollate,
		nCopies:     1,
	}

	ret, _, _ := procPrintDlgW.Call(uintptr(unsafe.Pointer(&pd)))
	if ret == 0 {
		code, _, _ := procCommDlgExtendedError.Call()
		freePrintHandles(pd.hDevMode, pd.hDevNames)
		if code == 0 {
			return nil, errPrintCancelled
		}
		return nil, fmt.Errorf("show print dialog failed: 0x%x", code)
	}
	if pd.hDC == 0 {
		freePrintHandles(pd.hDevMode, pd.hDevNames)
		return nil, errors.New("print dialog did not return printer device context")
	}
	applyDeviceModeToDC(pd.hDC, pd.hDevMode)

	return &printJob{
		hdc:      pd.hDC,
		hDevMode: pd.hDevMode,
		hDevName: pd.hDevNames,
	}, nil
}

func defaultPrintSettings(orientation string) (printDlg, error) {
	pd := printDlg{
		lStructSize: uint32(unsafe.Sizeof(printDlg{})),
		flags:       pdReturnDefault,
	}
	ret, _, _ := procPrintDlgW.Call(uintptr(unsafe.Pointer(&pd)))
	if ret == 0 {
		code, _, _ := procCommDlgExtendedError.Call()
		if code == 0 {
			return pd, errPrintCancelled
		}
		return pd, fmt.Errorf("load default print settings failed: 0x%x", code)
	}
	setDeviceModeOrientation(pd.hDevMode, orientation == "landscape")
	return pd, nil
}

func (j *printJob) cleanup() {
	if j.hdc != 0 {
		procDeleteDC.Call(uintptr(j.hdc))
	}
	freePrintHandles(j.hDevMode, j.hDevName)
}

func freePrintHandles(hDevMode, hDevNames windows.Handle) {
	if hDevMode != 0 {
		procGlobalFree.Call(uintptr(hDevMode))
	}
	if hDevNames != 0 {
		procGlobalFree.Call(uintptr(hDevNames))
	}
}

func setDeviceModeOrientation(hDevMode windows.Handle, landscape bool) {
	dm := lockDevMode(hDevMode)
	if dm == nil {
		return
	}
	defer unlockDevMode(hDevMode)

	dm.dmFields |= dmOrientationField
	if landscape {
		dm.dmOrientation = dmOrientLandscape
	} else {
		dm.dmOrientation = dmOrientPortrait
	}
}

func applyDeviceModeToDC(hdc windows.Handle, hDevMode windows.Handle) {
	dm := lockDevMode(hDevMode)
	if dm == nil {
		return
	}
	defer unlockDevMode(hDevMode)
	procResetDCW.Call(uintptr(hdc), uintptr(unsafe.Pointer(dm)))
}

func lockDevMode(hDevMode windows.Handle) *devMode {
	if hDevMode == 0 {
		return nil
	}
	ptr, _, _ := procGlobalLock.Call(uintptr(hDevMode))
	if ptr == 0 {
		return nil
	}
	return (*devMode)(unsafe.Pointer(ptr))
}

func unlockDevMode(hDevMode windows.Handle) {
	procGlobalUnlock.Call(uintptr(hDevMode))
}

func printImageToDC(hdc windows.Handle, docName string, img *image.RGBA, orientation string) error {
	name, err := windows.UTF16PtrFromString(docName)
	if err != nil {
		return err
	}
	info := docInfo{
		cbSize:      int32(unsafe.Sizeof(docInfo{})),
		lpszDocName: name,
	}

	if ret, _, _ := procStartDocW.Call(uintptr(hdc), uintptr(unsafe.Pointer(&info))); int32(ret) <= 0 {
		return errors.New("start print document failed")
	}
	docStarted := true
	defer func() {
		if docStarted {
			procEndDoc.Call(uintptr(hdc))
		}
	}()

	if ret, _, _ := procStartPage.Call(uintptr(hdc)); int32(ret) <= 0 {
		return errors.New("start print page failed")
	}

	if err := stretchImageToPage(hdc, img, orientation); err != nil {
		return err
	}

	if ret, _, _ := procEndPage.Call(uintptr(hdc)); int32(ret) <= 0 {
		return errors.New("end print page failed")
	}
	if ret, _, _ := procEndDoc.Call(uintptr(hdc)); int32(ret) <= 0 {
		docStarted = false
		return errors.New("end print document failed")
	}
	docStarted = false
	return nil
}

func stretchImageToPage(hdc windows.Handle, img *image.RGBA, orientation string) error {
	pageWidth := deviceCap(hdc, horzRes)
	pageHeight := deviceCap(hdc, vertRes)
	if pageWidth <= 0 || pageHeight <= 0 {
		return errors.New("printer page size is invalid")
	}

	srcWidth := img.Bounds().Dx()
	srcHeight := img.Bounds().Dy()
	if shouldRotateForPage(srcWidth, srcHeight, orientation) {
		img = rotateRGBA90(img)
		srcWidth = img.Bounds().Dx()
		srcHeight = img.Bounds().Dy()
	}
	destWidth, destHeight := fitInside(srcWidth, srcHeight, pageWidth, pageHeight)
	destX := (pageWidth - destWidth) / 2
	destY := (pageHeight - destHeight) / 2

	pixels := rgbaToBGRA(img)
	header := bitmapInfo{
		bmiHeader: bitmapInfoHeader{
			biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
			biWidth:       int32(srcWidth),
			biHeight:      -int32(srcHeight),
			biPlanes:      1,
			biBitCount:    32,
			biCompression: biRGB,
			biSizeImage:   uint32(len(pixels)),
		},
	}

	procSetStretchBltMode.Call(uintptr(hdc), halftone)
	ret, _, _ := procStretchDIBits.Call(
		uintptr(hdc),
		uintptr(destX),
		uintptr(destY),
		uintptr(destWidth),
		uintptr(destHeight),
		0,
		0,
		uintptr(srcWidth),
		uintptr(srcHeight),
		uintptr(unsafe.Pointer(&pixels[0])),
		uintptr(unsafe.Pointer(&header)),
		dibRGBColors,
		srcCopy,
	)
	if int32(ret) == spError {
		return errors.New("render image to printer failed")
	}
	return nil
}

func deviceCap(hdc windows.Handle, index int32) int {
	ret, _, _ := procGetDeviceCaps.Call(uintptr(hdc), uintptr(index))
	return int(int32(ret))
}

func fitInside(srcWidth, srcHeight, maxWidth, maxHeight int) (int, int) {
	width := maxWidth
	height := srcHeight * width / srcWidth
	if height > maxHeight {
		height = maxHeight
		width = srcWidth * height / srcHeight
	}
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	return width, height
}

func shouldRotateForPage(srcWidth, srcHeight int, orientation string) bool {
	return (orientation == "landscape" && srcWidth < srcHeight) || (orientation == "portrait" && srcWidth > srcHeight)
}

func rotateRGBA90(src *image.RGBA) *image.RGBA {
	srcBounds := src.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, srcHeight, srcWidth))

	for y := 0; y < srcHeight; y++ {
		for x := 0; x < srcWidth; x++ {
			dstX := srcHeight - 1 - y
			dstY := x
			srcOffset := src.PixOffset(srcBounds.Min.X+x, srcBounds.Min.Y+y)
			dstOffset := dst.PixOffset(dstX, dstY)
			copy(dst.Pix[dstOffset:dstOffset+4], src.Pix[srcOffset:srcOffset+4])
		}
	}
	return dst
}

func rgbaToBGRA(img *image.RGBA) []byte {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	pixels := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		srcRow := img.Pix[y*img.Stride:]
		dstRow := pixels[y*width*4:]
		for x := 0; x < width; x++ {
			src := x * 4
			dst := x * 4
			dstRow[dst+0] = srcRow[src+2]
			dstRow[dst+1] = srcRow[src+1]
			dstRow[dst+2] = srcRow[src+0]
			dstRow[dst+3] = srcRow[src+3]
		}
	}
	return pixels
}
