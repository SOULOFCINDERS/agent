//go:build ignore

package main

import (
	"fmt"
	"regexp"
	"strings"
)

var actionPairPattern = regexp.MustCompile(`(?:应该|建议|需要|必须|当然|肯定|那你|你就|那就|适合)\s*([^\n，。,\.]{2,20})`)

func main() {
	reply := "这是一个需要逻辑推理的问题。\n\n分析：\n- 距离仅50米，步行很近\n- 但是去洗车店的目的是洗车，你当然需要开车去把车带到洗车店\n\n如果你车就停在家门口，且准备直接开进去洗，那你当然开车去。\n\n✅ 结论：\n走路去 -- 更快、更省事。"

	markers := []string{"结论", "所以", "因此", "总结", "✅ 结论"}
	conclusionStart := -1
	for _, m := range markers {
		idx := strings.LastIndex(strings.ToLower(reply), strings.ToLower(m))
		if idx > conclusionStart {
			conclusionStart = idx
			fmt.Printf("Marker '%s' at byte %d\n", m, idx)
		}
	}
	fmt.Printf("conclusionStart=%d, len=%d, len/4=%d\n", conclusionStart, len(reply), len(reply)/4)

	if conclusionStart >= 0 && conclusionStart >= len(reply)/4 {
		reasoning := reply[:conclusionStart]
		conclusion := reply[conclusionStart:]
		fmt.Printf("\n--- REASONING (len=%d) ---\n%s\n", len(reasoning), reasoning)
		fmt.Printf("\n--- CONCLUSION (len=%d) ---\n%s\n", len(conclusion), conclusion)

		ra := actionPairPattern.FindAllStringSubmatch(reasoning, -1)
		ca := actionPairPattern.FindAllStringSubmatch(conclusion, -1)
		fmt.Printf("\nReasoning actions: %v\n", ra)
		fmt.Printf("Conclusion actions: %v\n", ca)
	}
}
