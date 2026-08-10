package service

import (
	"crypto/sha256"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// geminiImageOutputCounterKey 是请求级内联图片计数器挂在 gin.Context 上的键。
const geminiImageOutputCounterKey = "gemini_image_output_counter"

// geminiImageOutputCounter 记录一次转发里 Gemini 上游真正回吐的内联图片数量。
//
// 为了同时兼容累积式和增量式 SSE，按图片内容去重，并保存同一内容在单个
// payload 内出现的最大次数。这样累积 chunk 不会重复计费，而不同 chunk 中新
// 回吐的图片仍会计入实际张数。
type geminiImageOutputCounter struct {
	count  int
	images map[[sha256.Size]byte]int
}

// beginGeminiImageOutputObservation 在每次 Forward 开头重置计数器。
// failover 会拿同一个 gin.Context 重跑转发，不重置就会把上一个账号的图数带进来。
func beginGeminiImageOutputObservation(c *gin.Context) *geminiImageOutputCounter {
	if c == nil {
		return nil
	}
	counter := &geminiImageOutputCounter{images: make(map[[sha256.Size]byte]int)}
	c.Set(geminiImageOutputCounterKey, counter)
	return counter
}

func geminiImageOutputCounterFromContext(c *gin.Context) *geminiImageOutputCounter {
	if c == nil {
		return nil
	}
	value, ok := c.Get(geminiImageOutputCounterKey)
	if !ok {
		return nil
	}
	counter, _ := value.(*geminiImageOutputCounter)
	return counter
}

// observeGeminiImageOutputs 观测一段上游响应（整份或单个 chunk）里的内联图片。
// 调用点与 upstreamResponseModelObserver.ObserveGemini 一一对应——那里拿得到
// 解包后的上游响应体，这里需要的是同一份字节。
func observeGeminiImageOutputs(c *gin.Context, payload []byte) {
	counter := geminiImageOutputCounterFromContext(c)
	if counter == nil {
		return
	}
	for image, count := range countGeminiInlineImageOutputCounts(payload) {
		if count > counter.images[image] {
			counter.count += count - counter.images[image]
			counter.images[image] = count
		}
	}
}

func observedGeminiImageOutputs(c *gin.Context) int {
	counter := geminiImageOutputCounterFromContext(c)
	if counter == nil {
		return 0
	}
	return counter.count
}

// resolveGeminiImageCount 决定本次请求按几张图计费。
//
// 优先用上游真正返回的内联图片数：走 GeminiMessagesCompatService 的账号多是
// API Key + 自定义模型映射，客户端请求名和上游模型名都可能是站长自取的别名
// （issue #5358 里的 nana-banana-2），isImageGenerationModel 的白名单必然判不出，
// 于是 ImageCount=0，calculateRecordUsageCost 整条按次计费分支不触发，
// 生图请求全部记 $0。
//
// 只有响应里数不出图时（例如上游用 fileData 引用而非 inlineData 回图，或聚合
// 函数丢掉了图片 part）才退回既有的模型名启发式，保证老行为不回退；这里额外
// 也认映射后的上游模型名，与 shouldSkipCodexPlanGatedImageModelCooldown 对
// requestedModel / modelKey 双取的口径一致。
func resolveGeminiImageCount(c *gin.Context, originalModel, mappedModel string) int {
	if observed := observedGeminiImageOutputs(c); observed > 0 {
		return observed
	}
	if isImageGenerationModel(originalModel) || isImageGenerationModel(mappedModel) {
		return 1
	}
	return 0
}

// countGeminiInlineImageOutputs 统计一段 Gemini 响应 JSON 里的内联图片 part。
// Gemini REST 回 camelCase 的 inlineData，官方 SDK 与部分中转会回 snake_case
// 的 inline_data，两种都要认。
func countGeminiInlineImageOutputs(payload []byte) int {
	count := 0
	for _, occurrences := range countGeminiInlineImageOutputCounts(payload) {
		count += occurrences
	}
	return count
}

func countGeminiInlineImageOutputCounts(payload []byte) map[[sha256.Size]byte]int {
	images := make(map[[sha256.Size]byte]int)
	if len(payload) == 0 || !gjson.ValidBytes(payload) {
		return images
	}
	gjson.GetBytes(payload, "candidates").ForEach(func(_, candidate gjson.Result) bool {
		candidate.Get("content.parts").ForEach(func(_, part gjson.Result) bool {
			if image, ok := geminiInlineImageOutputKey(part); ok {
				images[image]++
			}
			return true
		})
		return true
	})
	return images
}

func geminiInlineImageOutputKey(part gjson.Result) ([sha256.Size]byte, bool) {
	inline := part.Get("inlineData")
	if !inline.Exists() {
		inline = part.Get("inline_data")
	}
	if !inline.Exists() {
		return [sha256.Size]byte{}, false
	}

	mimeType := inline.Get("mimeType")
	if !mimeType.Exists() {
		mimeType = inline.Get("mime_type")
	}
	mimeTypeValue := strings.ToLower(strings.TrimSpace(mimeType.String()))
	if !isGeminiInlineImageMIMEType(mimeTypeValue) {
		return [sha256.Size]byte{}, false
	}

	// 只认真的带上了 base64 数据的 part，空壳 part 不计费。
	data := strings.TrimSpace(inline.Get("data").String())
	if data == "" {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256([]byte(mimeTypeValue + "\x00" + data)), true
}
