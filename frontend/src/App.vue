<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { Dialogs } from '@wailsio/runtime'
import { PhotoService } from '../bindings/photomark2'

type ImageInfo = {
  path: string
  name: string
  size: number
  mimeType: string
  width: number
  height: number
  exif: Record<string, string>
}

type PaperPreset = {
  id: string
  name: string
  widthMm: number
  heightMm: number
}

type DrawMetrics = {
  canvasWidth: number
  canvasHeight: number
  paperX: number
  paperY: number
  paperW: number
  paperH: number
  imageX: number
  imageY: number
  imageW: number
  imageH: number
}

type LoadedImage = {
  info: ImageInfo
  dataURL: string
}

type SavedImage = {
  path: string
  name: string
  size: number
}

type PhotoQueueItem = {
  id: string
  name: string
  path?: string
  file?: File
  info?: ImageInfo
  address?: AmapAddress | null
  outputPath?: string
  error?: string
}

type AmapAddress = {
  formattedAddress: string
  province: string
  city: string
  district: string
  township: string
  street: string
  number: string
  adcode: string
  citycode: string
  location: string
}

const paperPresets: PaperPreset[] = [
  { id: '4in', name: '4寸 3x4', widthMm: 101.6, heightMm: 76.2 },
  { id: '6in', name: '6寸 4x6', widthMm: 152.4, heightMm: 101.6 },
  { id: '7in', name: '7寸 5x7', widthMm: 177.8, heightMm: 127 },
  { id: 'a4', name: 'A4', widthMm: 210, heightMm: 297 },
  { id: 'square', name: '方形 1:1', widthMm: 150, heightMm: 150 },
]

const fitModes = [
  { id: 'contain', name: '留白适配' },
  { id: 'cover', name: '裁切填满' },
] as const

const sourceInput = ref<HTMLInputElement | null>(null)
const canvasRef = ref<HTMLCanvasElement | null>(null)
const previewWrap = ref<HTMLElement | null>(null)
const imageEl = ref<HTMLImageElement | null>(null)
const imageURL = ref('')
const imageInfo = ref<ImageInfo | null>(null)
const selectedPaper = ref('6in')
const fitMode = ref<(typeof fitModes)[number]['id']>('contain')
const isDraggingFile = ref(false)
const isDraggingWatermark = ref(false)
const busy = ref(false)
const status = ref('导入照片后，可拖动水印并直接打印新生成图片。')
const lastExportPath = ref('')
const photoQueue = ref<PhotoQueueItem[]>([])
const currentQueueIndex = ref(-1)
const batchBusy = ref(false)
const batchCancelRequested = ref(false)
const batchProgress = ref({ current: 0, total: 0 })
const amapKey = ref('')
const addressBusy = ref(false)
const amapAddress = ref<AmapAddress | null>(null)
const previewSize = reactive({ width: 920, height: 650 })

const watermark = reactive({
  enabled: true,
  text: '',
  color: '#ffffff',
  shadowColor: '#111827',
  fontSize: 32,
  x: 0.72,
  y: 0.88,
})

const watermarkFields = reactive({
  time: true,
  location: true,
  gps: false,
  camera: false,
})

let dragOffset = { x: 0, y: 0 }
let drawMetrics: DrawMetrics | null = null

const currentPaper = computed(() => paperPresets.find((item) => item.id === selectedPaper.value) ?? paperPresets[0])
const paperRatio = computed(() => currentPaper.value.widthMm / currentPaper.value.heightMm)
const currentQueueItem = computed(() => photoQueue.value[currentQueueIndex.value] ?? null)
const queueLoadedCount = computed(() => photoQueue.value.filter((item) => item.outputPath).length)
const exifEntries = computed(() => {
  if (!imageInfo.value?.exif) return []
  const entries = Object.entries(imageInfo.value.exif).filter(([, value]) => value)
  if (amapAddress.value?.formattedAddress) {
    entries.push(['高德地址', amapAddress.value.formattedAddress])
  }
  return entries
})

const photoTime = computed(() => {
  const exif = imageInfo.value?.exif ?? {}
  return exif['拍摄时间'] || exif['修改时间'] || ''
})

const photoLocation = computed(() => {
  return amapAddress.value?.formattedAddress || imageInfo.value?.exif?.['GPS 坐标'] || ''
})

const cameraLabel = computed(() => {
  const exif = imageInfo.value?.exif ?? {}
  return [exif['相机品牌'], exif['相机型号']].filter(Boolean).join(' ')
})

const selectedWatermarkText = computed(() => {
  const pieces: string[] = []
  if (watermarkFields.time) pieces.push(photoTime.value)
  if (watermarkFields.location) pieces.push(photoLocation.value)
  if (watermarkFields.gps) pieces.push(imageInfo.value?.exif?.['GPS 坐标'] || '')
  if (watermarkFields.camera) pieces.push(cameraLabel.value)
  return pieces.filter(Boolean).join('  |  ')
})

const suggestedWatermark = computed(() => {
  const pieces = [selectedWatermarkText.value, imageInfo.value?.name, '相片打印助手']
  return pieces.filter(Boolean).join('  |  ')
})

const gpsCoordinate = computed(() => {
  return parseGPSCoordinate(imageInfo.value)
})

function parseGPSCoordinate(info: ImageInfo | null | undefined) {
  const value = info?.exif?.['GPS 坐标']
  if (!value) return null
  const match = value.match(/(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)/)
  if (!match) return null
  const latitude = Number(match[1])
  const longitude = Number(match[2])
  if (!Number.isFinite(latitude) || !Number.isFinite(longitude)) return null
  return { latitude, longitude }
}

async function triggerFilePicker() {
  try {
    const paths = await Dialogs.OpenFile({
      Title: '选择照片',
      Message: '选择一张或多张用于预览、加水印和批量处理的照片。',
      ButtonText: '选择',
      CanChooseFiles: true,
      CanChooseDirectories: false,
      AllowsMultipleSelection: true,
      Filters: [
        { DisplayName: '图片文件', Pattern: '*.jpg;*.jpeg;*.png;*.tif;*.tiff;*.bmp;*.webp' },
      ],
    })
    if (paths.length) await addNativeImages(paths)
  } catch (error) {
    status.value = errorMessage(error)
  }
}

async function onFileInput(event: Event) {
  const target = event.target as HTMLInputElement
  const files = Array.from(target.files ?? [])
  if (files.length) await addBrowserFiles(files)
  target.value = ''
}

async function onDrop(event: DragEvent) {
  event.preventDefault()
  isDraggingFile.value = false

  const files = Array.from(event.dataTransfer?.files ?? [])
  if (!files.length) return
  await addBrowserFiles(files)
}

async function addNativeImages(paths: string[]) {
  const startIndex = photoQueue.value.length
  paths.forEach((path) => {
    photoQueue.value.push({ id: queueItemID(path), name: fileNameFromPath(path), path })
  })
  status.value = `已加入 ${paths.length} 张照片。`
  if (startIndex >= 0) await selectQueueItem(startIndex)
}

async function addBrowserFiles(files: File[]) {
  const imageFiles = files.filter((file) => file.type.startsWith('image/'))
  if (!imageFiles.length) {
    status.value = '请选择图片文件。'
    return
  }

  const startIndex = photoQueue.value.length
  imageFiles.forEach((file) => {
    const path = browserFilePath(file)
    photoQueue.value.push({ id: queueItemID(path || file.name), name: file.name, path: path || undefined, file })
  })
  status.value = `已加入 ${imageFiles.length} 张照片。`
  await selectQueueItem(startIndex)
}

async function selectQueueItem(index: number) {
  const item = photoQueue.value[index]
  if (!item) return
  currentQueueIndex.value = index
  await loadQueueItem(item)
}

async function loadQueueItem(item: PhotoQueueItem) {
  if (item.path) {
    await loadNativeImage(item.path)
    item.info = imageInfo.value ?? undefined
    item.address = amapAddress.value
    item.error = undefined
    return
  }

  if (item.file) {
    await loadBrowserFile(item.file)
    item.info = imageInfo.value ?? undefined
    item.address = amapAddress.value
  }
}

async function loadBrowserFile(file: File) {
  if (!file.type.startsWith('image/')) {
    status.value = '请选择图片文件。'
    return
  }

  releaseImageURL()
  imageURL.value = URL.createObjectURL(file)
  imageEl.value = await loadImage(imageURL.value)
  status.value = `已加载 ${file.name}，正在读取元数据。`

  const path = browserFilePath(file)
  if (path) {
    await loadMetadata(path)
  } else {
    imageInfo.value = {
      path: '',
      name: file.name,
      size: file.size,
      mimeType: file.type,
      width: imageEl.value.naturalWidth,
      height: imageEl.value.naturalHeight,
      exif: { 解析状态: '浏览器未暴露本地绝对路径，拖拽到 Wails 窗口或使用系统文件选择可读取后端 EXIF。' },
    }
  }

  amapAddress.value = currentQueueItem.value?.address ?? null
  applySuggestedWatermark()
  await nextTick()
  drawPreview()
}

async function loadNativeImage(path: string) {
  try {
    busy.value = true
    status.value = '正在读取图片与 EXIF。'
    releaseImageURL()
    const loaded = (await PhotoService.LoadImage(path)) as LoadedImage
    imageInfo.value = loaded.info
    amapAddress.value = currentQueueItem.value?.path === path ? currentQueueItem.value.address ?? null : null
    imageURL.value = loaded.dataURL
    imageEl.value = await loadImage(loaded.dataURL)
    status.value = `已读取 ${loaded.info.name} 的图片信息。`
    applySuggestedWatermark()
    await nextTick()
    drawPreview()
  } catch (error) {
    status.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

async function loadMetadata(path: string) {
  try {
    imageInfo.value = (await PhotoService.ReadImage(path)) as ImageInfo
    status.value = `已读取 ${imageInfo.value.name} 的图片信息。`
  } catch (error) {
    status.value = errorMessage(error)
  }
}

function browserFilePath(file: File) {
  const candidate = file as File & { path?: string }
  return candidate.path ?? ''
}

function queueItemID(seed: string) {
  return `${Date.now()}-${Math.random().toString(16).slice(2)}-${seed}`
}

function fileNameFromPath(path: string) {
  return path.split(/[\\/]/).filter(Boolean).pop() || path
}

function syncCurrentQueueMetadata() {
  const item = currentQueueItem.value
  if (!item) return
  item.info = imageInfo.value ?? undefined
  item.address = amapAddress.value
}

function applySuggestedWatermark() {
  watermark.text = selectedWatermarkText.value || imageInfo.value?.name || '相片打印助手'
}

async function fetchAmapAddress() {
  const coordinate = gpsCoordinate.value
  if (!coordinate) {
    status.value = '当前照片没有可用的 GPS 坐标。'
    return
  }

  try {
    addressBusy.value = true
    status.value = '正在通过高德解析拍摄地址。'
    const address = (await PhotoService.ReverseGeocodeAmap(coordinate.latitude, coordinate.longitude, amapKey.value)) as AmapAddress
    amapAddress.value = address
    syncCurrentQueueMetadata()
    if (watermarkFields.location) applySuggestedWatermark()
    status.value = `已获取地址：${address.formattedAddress}`
  } catch (error) {
    status.value = errorMessage(error)
  } finally {
    addressBusy.value = false
  }
}

function appendAddressToWatermark() {
  const address = amapAddress.value?.formattedAddress
  if (!address) return
  const current = watermark.text.trim()
  watermark.text = current ? `${current}  |  ${address}` : address
}

function loadImage(src: string) {
  return new Promise<HTMLImageElement>((resolve, reject) => {
    const img = new Image()
    img.onload = () => resolve(img)
    img.onerror = () => reject(new Error('图片加载失败'))
    img.src = src
  })
}

function drawPreview() {
  const canvas = canvasRef.value
  const img = imageEl.value
  if (!canvas || !img) return

  const dpr = window.devicePixelRatio || 1
  const metrics = calculateMetrics(previewSize.width, previewSize.height)
  drawMetrics = metrics
  canvas.width = Math.round(metrics.canvasWidth * dpr)
  canvas.height = Math.round(metrics.canvasHeight * dpr)
  canvas.style.width = `${metrics.canvasWidth}px`
  canvas.style.height = `${metrics.canvasHeight}px`

  const ctx = canvas.getContext('2d')
  if (!ctx) return
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
  renderToContext(ctx, metrics, img, true)
}

function calculateMetrics(maxWidth: number, maxHeight: number): DrawMetrics {
  const ratio = paperRatio.value
  let paperW = maxWidth
  let paperH = paperW / ratio
  if (paperH > maxHeight) {
    paperH = maxHeight
    paperW = paperH * ratio
  }

  const canvasWidth = Math.round(Math.max(320, paperW + 44))
  const canvasHeight = Math.round(Math.max(260, paperH + 44))
  const paperX = (canvasWidth - paperW) / 2
  const paperY = (canvasHeight - paperH) / 2
  const img = imageEl.value
  const imageRatio = img ? img.naturalWidth / img.naturalHeight : ratio
  const scale = fitMode.value === 'cover' ? Math.max(paperW / (img?.naturalWidth || paperW), paperH / (img?.naturalHeight || paperH)) : Math.min(paperW / (img?.naturalWidth || paperW), paperH / (img?.naturalHeight || paperH))
  const imageW = img ? img.naturalWidth * scale : paperW
  const imageH = img ? img.naturalHeight * scale : paperH
  const fallbackW = imageRatio >= ratio ? paperW : paperH * imageRatio
  const fallbackH = imageRatio >= ratio ? paperW / imageRatio : paperH

  return {
    canvasWidth,
    canvasHeight,
    paperX,
    paperY,
    paperW,
    paperH,
    imageX: paperX + (paperW - (img ? imageW : fallbackW)) / 2,
    imageY: paperY + (paperH - (img ? imageH : fallbackH)) / 2,
    imageW: img ? imageW : fallbackW,
    imageH: img ? imageH : fallbackH,
  }
}

function renderToContext(ctx: CanvasRenderingContext2D, metrics: DrawMetrics, img: HTMLImageElement, showGuides: boolean) {
  ctx.clearRect(0, 0, metrics.canvasWidth, metrics.canvasHeight)

  ctx.fillStyle = '#E1E5EA'
  ctx.fillRect(0, 0, metrics.canvasWidth, metrics.canvasHeight)

  ctx.fillStyle = '#ffffff'
  ctx.fillRect(metrics.paperX, metrics.paperY, metrics.paperW, metrics.paperH)

  ctx.save()
  ctx.beginPath()
  ctx.rect(metrics.paperX, metrics.paperY, metrics.paperW, metrics.paperH)
  ctx.clip()
  ctx.fillStyle = '#ffffff'
  ctx.fillRect(metrics.paperX, metrics.paperY, metrics.paperW, metrics.paperH)
  ctx.drawImage(img, metrics.imageX, metrics.imageY, metrics.imageW, metrics.imageH)
  drawWatermark(ctx, metrics)
  ctx.restore()

  if (showGuides) {
    ctx.strokeStyle = '#8C98A7'
    ctx.lineWidth = 1
    ctx.strokeRect(metrics.paperX + 0.5, metrics.paperY + 0.5, metrics.paperW - 1, metrics.paperH - 1)
    ctx.strokeStyle = fitMode.value === 'cover' ? 'rgba(217, 108, 79, 0.9)' : 'rgba(71, 106, 128, 0.72)'
    ctx.setLineDash([7, 7])
    ctx.strokeRect(metrics.paperX + 8.5, metrics.paperY + 8.5, metrics.paperW - 17, metrics.paperH - 17)
    ctx.setLineDash([])
  }
}

function drawWatermark(ctx: CanvasRenderingContext2D, metrics: DrawMetrics) {
  if (!watermark.enabled || !watermark.text.trim()) return

  const fontSize = watermark.fontSize
  const x = metrics.paperX + watermark.x * metrics.paperW
  const y = metrics.paperY + watermark.y * metrics.paperH

  ctx.font = `600 ${fontSize}px Inter, system-ui, sans-serif`
  ctx.textAlign = 'center'
  ctx.textBaseline = 'middle'
  ctx.shadowColor = watermark.shadowColor
  ctx.shadowBlur = Math.max(4, fontSize * 0.18)
  ctx.shadowOffsetY = Math.max(2, fontSize * 0.08)
  ctx.fillStyle = watermark.color
  wrapWatermarkLines(ctx, watermark.text, metrics.paperW * 0.82).forEach((line, index, lines) => {
    const lineY = y + (index - (lines.length - 1) / 2) * fontSize * 1.25
    ctx.fillText(line, x, lineY)
  })
  ctx.shadowBlur = 0
  ctx.shadowOffsetY = 0
}

function wrapWatermarkLines(ctx: CanvasRenderingContext2D, text: string, maxWidth: number) {
  const parts = text.split(/\s+/).filter(Boolean)
  if (parts.length === 0) return ['']
  const lines: string[] = []
  let line = parts[0]
  for (let i = 1; i < parts.length; i++) {
    const test = `${line} ${parts[i]}`
    if (ctx.measureText(test).width <= maxWidth) {
      line = test
    } else {
      lines.push(line)
      line = parts[i]
    }
  }
  lines.push(line)
  return lines.slice(0, 3)
}

function pointerInWatermark(event: PointerEvent) {
  if (!drawMetrics || !canvasRef.value) return false
  const point = eventToCanvasPoint(event)
  const textCenter = watermarkCenter(drawMetrics)
  const fontSize = watermark.fontSize
  const width = Math.min(drawMetrics.paperW * 0.86, Math.max(180, watermark.text.length * fontSize * 0.32))
  const height = fontSize * 2.4
  return Math.abs(point.x - textCenter.x) <= width / 2 && Math.abs(point.y - textCenter.y) <= height / 2
}

function watermarkCenter(metrics: DrawMetrics) {
  return {
    x: metrics.paperX + watermark.x * metrics.paperW,
    y: metrics.paperY + watermark.y * metrics.paperH,
  }
}

function onPointerDown(event: PointerEvent) {
  if (!drawMetrics || !pointerInWatermark(event)) return
  isDraggingWatermark.value = true
  const point = eventToCanvasPoint(event)
  const center = watermarkCenter(drawMetrics)
  dragOffset = { x: point.x - center.x, y: point.y - center.y }
  canvasRef.value?.setPointerCapture(event.pointerId)
}

function onPointerMove(event: PointerEvent) {
  if (!isDraggingWatermark.value || !drawMetrics) return
  const point = eventToCanvasPoint(event)
  const x = (point.x - dragOffset.x - drawMetrics.paperX) / drawMetrics.paperW
  const y = (point.y - dragOffset.y - drawMetrics.paperY) / drawMetrics.paperH
  watermark.x = clamp(x, 0.05, 0.95)
  watermark.y = clamp(y, 0.08, 0.94)
  drawPreview()
}

function onPointerUp(event: PointerEvent) {
  if (!isDraggingWatermark.value) return
  isDraggingWatermark.value = false
  canvasRef.value?.releasePointerCapture(event.pointerId)
}

function eventToCanvasPoint(event: PointerEvent) {
  const rect = canvasRef.value!.getBoundingClientRect()
  return {
    x: event.clientX - rect.left,
    y: event.clientY - rect.top,
  }
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value))
}

async function exportDataURL(targetImage = imageEl.value, watermarkText = watermark.text) {
  const img = targetImage
  if (!img) throw new Error('请先导入图片')

  const exportWidth = Math.round(currentPaper.value.widthMm * 12)
  const exportHeight = Math.round(currentPaper.value.heightMm * 12)
  const canvas = document.createElement('canvas')
  canvas.width = exportWidth
  canvas.height = exportHeight
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new Error('无法创建导出画布')

  const metrics: DrawMetrics = {
    canvasWidth: exportWidth,
    canvasHeight: exportHeight,
    paperX: 0,
    paperY: 0,
    paperW: exportWidth,
    paperH: exportHeight,
    imageX: 0,
    imageY: 0,
    imageW: exportWidth,
    imageH: exportHeight,
  }

  const scale = fitMode.value === 'cover' ? Math.max(exportWidth / img.naturalWidth, exportHeight / img.naturalHeight) : Math.min(exportWidth / img.naturalWidth, exportHeight / img.naturalHeight)
  metrics.imageW = img.naturalWidth * scale
  metrics.imageH = img.naturalHeight * scale
  metrics.imageX = (exportWidth - metrics.imageW) / 2
  metrics.imageY = (exportHeight - metrics.imageH) / 2

  const originalFontSize = watermark.fontSize
  const originalText = watermark.text
  watermark.fontSize = Math.round(originalFontSize * (exportWidth / (drawMetrics?.paperW || exportWidth)))
  watermark.text = watermarkText
  renderToContext(ctx, metrics, img, false)
  watermark.fontSize = originalFontSize
  watermark.text = originalText

  return canvas.toDataURL('image/png')
}

async function saveImage() {
  try {
    busy.value = true
    status.value = '正在保存合成图片。'
    const dataURL = await exportDataURL()
    const saved = await PhotoService.SaveRenderedImage(dataURL)
    if (!saved) throw new Error('保存合成图片失败')
    lastExportPath.value = saved.path
    if (currentQueueItem.value) currentQueueItem.value.outputPath = saved.path
    status.value = `已保存到临时文件：${saved.path}`
  } catch (error) {
    status.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

async function printImage() {
  try {
    busy.value = true
    status.value = '正在生成新图片并调起系统打印。'
    const dataURL = await exportDataURL()
    const saved = await PhotoService.PrintRenderedImage(dataURL)
    if (!saved) throw new Error('打印服务未返回输出文件')
    lastExportPath.value = saved.path
    status.value = `已发送打印任务，新图片保存在：${saved.path}`
  } catch (error) {
    status.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

async function processBatchImages() {
  const pendingItems = photoQueue.value.filter((item) => !item.outputPath)
  if (!pendingItems.length) {
    status.value = photoQueue.value.length ? '队列里的图片都已处理完成。' : '请先导入图片。'
    return
  }

  const originalIndex = currentQueueIndex.value
  batchCancelRequested.value = false
  batchBusy.value = true
  batchProgress.value = { current: 0, total: pendingItems.length }
  busy.value = true
  let success = 0
  let failed = 0

  try {
    for (let index = 0; index < pendingItems.length; index++) {
      if (batchCancelRequested.value) break
      const item = pendingItems[index]
      batchProgress.value.current = index + 1
      status.value = `正在批量处理 ${index + 1} / ${pendingItems.length}：${item.name}`

      try {
        const loaded = await loadItemForBatch(item)
        const text = buildWatermarkText(loaded.info, item.address ?? null) || item.name
        const dataURL = await exportDataURL(loaded.image, text)
        const saved = (await PhotoService.SaveRenderedImage(dataURL)) as SavedImage
        item.info = loaded.info
        item.outputPath = saved.path
        item.error = undefined
        success += 1
      } catch (error) {
        item.error = errorMessage(error)
        failed += 1
      }
    }

    const remaining = photoQueue.value.filter((item) => !item.outputPath).length
    status.value = batchCancelRequested.value
      ? `批量处理已停止：本次成功 ${success} 张，失败 ${failed} 张，剩余 ${remaining} 张可继续。`
      : `批量处理完成：本次成功 ${success} 张，失败 ${failed} 张，剩余 ${remaining} 张可继续。`
  } finally {
    batchBusy.value = false
    batchCancelRequested.value = false
    busy.value = false
    if (originalIndex >= 0) await selectQueueItem(originalIndex)
  }
}

function stopBatchProcessing() {
  batchCancelRequested.value = true
  status.value = '正在停止批量处理，当前图片完成后会暂停。'
}

async function loadItemForBatch(item: PhotoQueueItem) {
  if (item.path) {
    const loaded = (await PhotoService.LoadImage(item.path)) as LoadedImage
    const image = await loadImage(loaded.dataURL)
    let address = item.address ?? null
    if (watermarkFields.location && amapKey.value && parseGPSCoordinate(loaded.info)) {
      address = await loadAmapAddressForInfo(loaded.info, item.address ?? null)
      item.address = address
    }
    return { info: loaded.info, image, address }
  }

  if (!item.file) throw new Error('图片来源不可用')
  const url = URL.createObjectURL(item.file)
  try {
    const image = await loadImage(url)
    const info = item.info ?? {
      path: '',
      name: item.file.name,
      size: item.file.size,
      mimeType: item.file.type,
      width: image.naturalWidth,
      height: image.naturalHeight,
      exif: {},
    }
    return { info, image, address: item.address ?? null }
  } finally {
    URL.revokeObjectURL(url)
  }
}

async function loadAmapAddressForInfo(info: ImageInfo, cached: AmapAddress | null) {
  if (cached) return cached
  const coordinate = parseGPSCoordinate(info)
  if (!coordinate) return null
  try {
    return (await PhotoService.ReverseGeocodeAmap(coordinate.latitude, coordinate.longitude, amapKey.value)) as AmapAddress
  } catch {
    return null
  }
}

function buildWatermarkText(info: ImageInfo, address: AmapAddress | null) {
  const pieces: string[] = []
  if (watermarkFields.time) pieces.push(info.exif?.['拍摄时间'] || info.exif?.['修改时间'] || '')
  if (watermarkFields.location) pieces.push(address?.formattedAddress || info.exif?.['GPS 坐标'] || '')
  if (watermarkFields.gps) pieces.push(info.exif?.['GPS 坐标'] || '')
  if (watermarkFields.camera) pieces.push([info.exif?.['相机品牌'], info.exif?.['相机型号']].filter(Boolean).join(' '))
  return pieces.filter(Boolean).join('  |  ')
}

function updatePreviewSize() {
  const box = previewWrap.value
  if (!box) return
  const rect = box.getBoundingClientRect()
  previewSize.width = Math.max(480, rect.width - 40)
  previewSize.height = Math.max(360, rect.height - 40)
  drawPreview()
}

function releaseImageURL() {
  if (imageURL.value.startsWith('blob:')) URL.revokeObjectURL(imageURL.value)
  imageURL.value = ''
}

function formatBytes(size: number) {
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / 1024 / 1024).toFixed(1)} MB`
}

function errorMessage(error: unknown) {
  if (error instanceof Error) return error.message
  return String(error)
}

watch([selectedPaper, fitMode, () => watermark.text, () => watermark.color, () => watermark.shadowColor, () => watermark.fontSize, () => watermark.enabled], drawPreview)
watch([() => watermarkFields.time, () => watermarkFields.location, () => watermarkFields.gps, () => watermarkFields.camera], applySuggestedWatermark)
watch(amapKey, (value) => localStorage.setItem('photomark2.amapKey', value))

onMounted(() => {
  amapKey.value = localStorage.getItem('photomark2.amapKey') ?? ''
  updatePreviewSize()
  window.addEventListener('resize', updatePreviewSize)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', updatePreviewSize)
  releaseImageURL()
})
</script>

<template>
  <main class="flex h-full bg-[#ECEFF3] text-ink">
    <aside class="flex w-[320px] shrink-0 flex-col border-r border-[#D4DAE2] bg-[#F8FAFC]">
      <header class="border-b border-[#D4DAE2] px-5 py-5">
        <h1 class="text-xl font-bold tracking-normal">相片打印助手</h1>
        <p class="mt-2 text-sm leading-6 text-[#617184]">导入照片，添加元数据水印，按相纸比例输出新图片。</p>
      </header>

      <section class="space-y-3 border-b border-[#D4DAE2] p-5">
        <input ref="sourceInput" class="hidden" type="file" accept="image/*" multiple @change="onFileInput" />
        <button class="h-10 w-full rounded-[3px] bg-[#17202A] px-4 text-sm font-semibold text-white transition hover:bg-[#243142]" @click="triggerFilePicker">
          选择图片
        </button>

        <div
          class="border border-dashed px-4 py-5 text-center text-sm transition"
          :class="isDraggingFile ? 'border-mint bg-[#ECF7F2] text-[#2D7C5D]' : 'border-[#C8D0DA] bg-[#F3F5F8] text-[#6B7788]'"
          @dragover.prevent="isDraggingFile = true"
          @dragleave="isDraggingFile = false"
          @drop="onDrop"
        >
          将图片拖入这里
        </div>
      </section>

      <section v-if="photoQueue.length" class="border-b border-[#D4DAE2] p-5">
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-xs font-bold uppercase tracking-[0.08em] text-[#4C5A69]">图片队列</h2>
          <span class="text-xs text-[#6B7788]">{{ queueLoadedCount }} / {{ photoQueue.length }} 已完成</span>
        </div>
        <div class="max-h-[210px] space-y-2 overflow-auto">
          <button
            v-for="(item, index) in photoQueue"
            :key="item.id"
            class="w-full border px-3 py-2 text-left text-sm transition"
            :class="index === currentQueueIndex ? 'border-mint bg-[#ECF7F2]' : 'border-[#DDE2E8] bg-[#F3F5F8] hover:bg-[#EEF2F6]'"
            @click="selectQueueItem(index)"
          >
            <div class="truncate font-semibold text-[#2C3A48]">{{ item.name }}</div>
            <div class="mt-1 truncate text-xs" :class="item.error ? 'text-[#C85F45]' : item.outputPath ? 'text-[#2D7C5D]' : 'text-[#6B7788]'">
              {{ item.error || item.outputPath || '待处理' }}
            </div>
          </button>
        </div>
      </section>

      <section class="min-h-0 flex-1 overflow-auto p-5">
        <div class="mb-3 flex items-center justify-between">
          <h2 class="text-xs font-bold uppercase tracking-[0.08em] text-[#4C5A69]">图片元数据</h2>
          <span v-if="imageInfo" class="text-xs text-[#6B7788]">{{ imageInfo.width }} x {{ imageInfo.height }}</span>
        </div>

        <div v-if="imageInfo" class="mb-4 border-y border-[#DDE2E8] bg-[#F3F5F8] px-3 py-3 text-sm">
          <div class="font-semibold text-[#1D2A36]">{{ imageInfo.name }}</div>
          <div class="mt-1 text-xs text-[#6B7788]">{{ imageInfo.mimeType }} · {{ formatBytes(imageInfo.size) }}</div>
        </div>

        <dl v-if="exifEntries.length" class="divide-y divide-[#DDE2E8] border-y border-[#DDE2E8]">
          <div v-for="[key, value] in exifEntries" :key="key" class="grid grid-cols-[86px_1fr] gap-3 py-3">
            <dt class="text-xs font-semibold text-[#6B7788]">{{ key }}</dt>
            <dd class="break-words text-sm leading-5 text-[#1D2A36]">{{ value }}</dd>
          </div>
        </dl>
        <p v-else class="border-y border-[#DDE2E8] bg-[#F3F5F8] p-4 text-sm leading-6 text-[#6B7788]">尚未导入图片。</p>
      </section>
    </aside>

    <section class="flex min-w-0 flex-1 flex-col">
      <div class="flex h-14 shrink-0 items-center justify-between border-b border-[#D4DAE2] bg-[#F8FAFC] px-5">
        <div class="truncate pr-4 text-sm text-[#617184]">{{ status }}</div>
        <div class="flex items-center gap-2">
          <button v-if="!batchBusy" class="h-9 rounded-[3px] border border-[#C8D0DA] bg-[#F8FAFC] px-4 text-sm font-semibold text-[#2C3A48] hover:bg-[#EEF2F6]" :disabled="!photoQueue.length" @click="processBatchImages">
            批量处理
          </button>
          <button v-else class="h-9 rounded-[3px] border border-[#C8D0DA] bg-[#F8FAFC] px-4 text-sm font-semibold text-[#2C3A48] hover:bg-[#EEF2F6]" @click="stopBatchProcessing">
            停止
          </button>
          <button class="h-9 rounded-[3px] border border-[#C8D0DA] bg-[#F8FAFC] px-4 text-sm font-semibold text-[#2C3A48] hover:bg-[#EEF2F6]" :disabled="!imageEl || busy" @click="saveImage">
            保存新图片
          </button>
          <button class="h-9 rounded-[3px] bg-coral px-4 text-sm font-semibold text-white hover:bg-[#C85F45]" :disabled="!imageEl || busy" @click="printImage">
            确认打印
          </button>
        </div>
      </div>
      <div v-if="batchBusy" class="h-1 shrink-0 bg-[#DDE2E8]">
        <div class="h-full bg-mint transition-all" :style="{ width: `${batchProgress.total ? (batchProgress.current / batchProgress.total) * 100 : 0}%` }" />
      </div>

      <div class="grid min-h-0 flex-1 grid-cols-[1fr_340px]">
        <div
          ref="previewWrap"
          class="relative flex min-h-0 items-center justify-center overflow-hidden bg-[#E1E5EA] p-5"
          @dragover.prevent="isDraggingFile = true"
          @drop="onDrop"
        >
          <canvas
            v-show="imageEl"
            ref="canvasRef"
            class="touch-none rounded-sm"
            :class="isDraggingWatermark ? 'cursor-grabbing' : 'cursor-grab'"
            @pointerdown="onPointerDown"
            @pointermove="onPointerMove"
            @pointerup="onPointerUp"
            @pointercancel="onPointerUp"
          />
          <div v-if="!imageEl" class="w-[360px] border border-dashed border-[#B8C2CF] bg-[#E8ECF1] p-8 text-center">
            <div class="mx-auto mb-4 flex h-14 w-14 items-center justify-center border border-[#B8C2CF] bg-[#F8FAFC] text-3xl text-steel">+</div>
            <p class="text-base font-semibold text-[#2C3A48]">导入一张照片开始预览</p>
            <p class="mt-2 text-sm leading-6 text-[#617184]">支持按钮选择或拖拽导入。水印、裁切和打印只会生成新文件。</p>
          </div>
        </div>

        <aside class="overflow-auto border-l border-[#D4DAE2] bg-[#F8FAFC] p-5">
          <section class="space-y-4 border-b border-[#DDE2E8] pb-6">
            <h2 class="text-xs font-bold uppercase tracking-[0.08em] text-[#4C5A69]">水印</h2>
            <label class="flex items-center justify-between border-y border-[#DDE2E8] bg-[#F3F5F8] px-3 py-3 text-sm">
              <span class="font-medium text-[#2C3A48]">显示水印</span>
              <input v-model="watermark.enabled" type="checkbox" class="h-5 w-5 accent-mint" />
            </label>

            <label class="block text-sm">
              <span class="font-medium text-[#2C3A48]">水印文字</span>
              <textarea v-model="watermark.text" rows="4" class="mt-2 w-full resize-none rounded-[3px] border border-[#C8D0DA] bg-white p-3 text-sm outline-none focus:border-mint" />
            </label>

            <div class="space-y-2 border-y border-[#DDE2E8] bg-[#F3F5F8] p-3">
              <div class="flex items-center justify-between">
                <span class="text-sm font-semibold text-[#2C3A48]">水印内容</span>
                <span class="text-xs text-[#6B7788]">可多选</span>
              </div>
              <label class="flex items-center justify-between text-sm">
                <span class="text-[#2C3A48]">时间</span>
                <input v-model="watermarkFields.time" type="checkbox" class="h-5 w-5 accent-mint" />
              </label>
              <label class="flex items-center justify-between text-sm">
                <span class="text-[#2C3A48]">地点</span>
                <input v-model="watermarkFields.location" type="checkbox" class="h-5 w-5 accent-mint" />
              </label>
              <label class="flex items-center justify-between text-sm">
                <span class="text-[#2C3A48]">GPS 坐标</span>
                <input v-model="watermarkFields.gps" type="checkbox" class="h-5 w-5 accent-mint" />
              </label>
              <label class="flex items-center justify-between text-sm">
                <span class="text-[#2C3A48]">相机信息</span>
                <input v-model="watermarkFields.camera" type="checkbox" class="h-5 w-5 accent-mint" />
              </label>
            </div>

            <button class="h-9 w-full rounded-[3px] border border-[#C8D0DA] bg-[#F8FAFC] text-sm font-semibold text-[#2C3A48] hover:bg-[#EEF2F6]" :disabled="!suggestedWatermark" @click="applySuggestedWatermark">
              应用所选内容
            </button>

            <div class="space-y-3 border-y border-[#DDE2E8] bg-[#F3F5F8] p-3">
              <div class="flex items-center justify-between">
                <span class="text-sm font-semibold text-[#2C3A48]">拍摄地址</span>
                <span class="text-xs text-[#6B7788]">高德地图</span>
              </div>
              <input v-model="amapKey" type="password" autocomplete="off" placeholder="高德 Web 服务 API Key" class="h-10 w-full rounded-[3px] border border-[#C8D0DA] bg-white px-3 text-sm outline-none focus:border-mint" />
              <button class="h-9 w-full rounded-[3px] border border-[#C8D0DA] bg-[#F8FAFC] text-sm font-semibold text-[#2C3A48] hover:bg-[#EEF2F6] disabled:cursor-not-allowed disabled:opacity-55" :disabled="!gpsCoordinate || !amapKey || addressBusy" @click="fetchAmapAddress">
                获取中文地址
              </button>
              <div v-if="amapAddress" class="text-sm leading-6 text-[#2D7C5D]">
                {{ amapAddress.formattedAddress }}
              </div>
              <p v-else class="text-sm leading-6 text-[#6B7788]">
                {{ gpsCoordinate ? '读取照片 GPS 后，可通过高德解析为地址。' : '当前照片未读取到 GPS 坐标。' }}
              </p>
              <button class="h-9 w-full rounded-[3px] bg-mint text-sm font-semibold text-white hover:bg-[#2D7C5D] disabled:cursor-not-allowed disabled:opacity-55" :disabled="!amapAddress" @click="appendAddressToWatermark">
                添加地址到水印
              </button>
            </div>

            <div class="grid grid-cols-2 gap-3">
              <label class="block text-sm">
                <span class="font-medium text-[#2C3A48]">字体颜色</span>
                <input v-model="watermark.color" type="color" class="mt-2 h-10 w-full rounded-[3px] border border-[#C8D0DA] bg-white p-1" />
              </label>
              <label class="block text-sm">
                <span class="font-medium text-[#2C3A48]">阴影颜色</span>
                <input v-model="watermark.shadowColor" type="color" class="mt-2 h-10 w-full rounded-[3px] border border-[#C8D0DA] bg-white p-1" />
              </label>
            </div>

            <label class="block text-sm">
              <div class="flex items-center justify-between">
                <span class="font-medium text-[#2C3A48]">字号</span>
                <span class="text-xs text-[#6B7788]">{{ watermark.fontSize }} px</span>
              </div>
              <input v-model.number="watermark.fontSize" min="14" max="88" type="range" class="mt-2 w-full accent-mint" />
            </label>
          </section>

          <section class="mt-6 space-y-4">
            <h2 class="text-xs font-bold uppercase tracking-[0.08em] text-[#4C5A69]">相纸适配</h2>
            <label class="block text-sm">
              <span class="font-medium text-[#2C3A48]">相纸尺寸</span>
              <select v-model="selectedPaper" class="mt-2 h-10 w-full rounded-[3px] border border-[#C8D0DA] bg-white px-3 text-sm outline-none focus:border-mint">
                <option v-for="paper in paperPresets" :key="paper.id" :value="paper.id">
                  {{ paper.name }} · {{ paper.widthMm }} x {{ paper.heightMm }}mm
                </option>
              </select>
            </label>

            <div class="grid grid-cols-2 border border-[#C8D0DA] bg-[#EEF2F6]">
              <button
                v-for="mode in fitModes"
                :key="mode.id"
                class="h-9 text-sm font-semibold"
                :class="fitMode === mode.id ? 'bg-white text-ink' : 'text-[#617184] hover:text-[#2C3A48]'"
                @click="fitMode = mode.id"
              >
                {{ mode.name }}
              </button>
            </div>

            <div class="border-y border-[#DDE2E8] bg-[#F3F5F8] p-3 text-sm leading-6 text-[#617184]">
              当前输出：{{ currentPaper.name }}，比例 {{ paperRatio.toFixed(3) }}。预览虚线即最终相纸边界。
            </div>
          </section>

          <section v-if="lastExportPath" class="mt-6 border-y border-[#B8D8C9] bg-[#ECF7F2] p-3 text-sm leading-6 text-[#2D7C5D]">
            最新生成文件：{{ lastExportPath }}
          </section>
        </aside>
      </div>
    </section>
  </main>
</template>
