package rag

import (
	"strings"
	"unicode/utf8"
)

// TextChunker 基于文本边界的切分器
// 按段落 → 句子 → 字符数递归切分，尽量保持语义完整
type TextChunker struct{}

func NewTextChunker() *TextChunker {
	return &TextChunker{}
}

// Split 将文本切分为 chunks
// chunkSize: 每个 chunk 的目标字符数
// overlap: 相邻 chunk 之间重叠的字符数
func (c *TextChunker) Split(text string, chunkSize, overlap int) []string {
	if chunkSize <= 0 {
		chunkSize = 500
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 5
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// 如果文本短于一个 chunk，直接返回
	if utf8.RuneCountInString(text) <= chunkSize {
		return []string{text}
	}

	// 先按段落分隔
	paragraphs := splitParagraphs(text)

	var chunks []string
	var current strings.Builder
	currentLen := 0

	flush := func() {
		if current.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(current.String()))
			// overlap: 保留尾部 overlap 字符到下一个 chunk
			if overlap > 0 {
				content := current.String()
				runes := []rune(content)
				if len(runes) > overlap {
					current.Reset()
					current.WriteString(string(runes[len(runes)-overlap:]))
					currentLen = overlap
				} else {
					current.Reset()
					currentLen = 0
				}
			} else {
				current.Reset()
				currentLen = 0
			}
		}
	}

	for _, para := range paragraphs {
		paraLen := utf8.RuneCountInString(para)

		// 如果单个段落超过 chunkSize，按句子切分
		if paraLen > chunkSize {
			sentences := splitSentences(para)
			for _, sent := range sentences {
				sentLen := utf8.RuneCountInString(sent)
				if currentLen+sentLen > chunkSize && currentLen > 0 {
					flush()
				}
				// 超长句子硬切
				if sentLen > chunkSize {
					if currentLen > 0 {
						flush()
					}
					runes := []rune(sent)
					for i := 0; i < len(runes); i += chunkSize - overlap {
						end := i + chunkSize
						if end > len(runes) {
							end = len(runes)
						}
						chunks = append(chunks, strings.TrimSpace(string(runes[i:end])))
					}
					continue
				}
				if currentLen > 0 {
					current.WriteString(" ")
					currentLen++
				}
				current.WriteString(sent)
				currentLen += sentLen
			}
			continue
		}

		if currentLen+paraLen > chunkSize && currentLen > 0 {
			flush()
		}
		if currentLen > 0 {
			current.WriteString("\n\n")
			currentLen += 2
		}
		current.WriteString(para)
		currentLen += paraLen
	}

	if current.Len() > 0 {
		chunk := strings.TrimSpace(current.String())
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
	}

	return chunks
}

// splitParagraphs 按空行分隔段落
func splitParagraphs(text string) []string {
	raw := strings.Split(text, "\n\n")
	var result []string
	for _, p := range raw {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		// 没有空行分隔，按单个换行分
		raw = strings.Split(text, "\n")
		for _, p := range raw {
			p = strings.TrimSpace(p)
			if p != "" {
				result = append(result, p)
			}
		}
	}
	return result
}

// splitSentences 按句号/问号/感叹号等分句
func splitSentences(text string) []string {
	var sentences []string
	var current strings.Builder

	runes := []rune(text)
	for i, r := range runes {
		current.WriteRune(r)

		isSentEnd := false
		switch r {
		case '.', '!', '?', '。', '！', '？', '；':
			isSentEnd = true
		}

		if isSentEnd && (i == len(runes)-1 || runes[i+1] == ' ' || runes[i+1] == '\n') {
			s := strings.TrimSpace(current.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			current.Reset()
		}
	}
	if current.Len() > 0 {
		s := strings.TrimSpace(current.String())
		if s != "" {
			sentences = append(sentences, s)
		}
	}

	return sentences
}
