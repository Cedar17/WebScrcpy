package scrcpy

import "fmt"

func scrcpyParamsToArgs(params map[string]string) []string {
	var args []string
	// keys := []string{
	// 	"scid",
	// 	"max_fps",
	// 	"video",
	// 	"video_codec",
	// 	"video_bit_rate",
	// 	"video_codec_options",
	// 	"video_encoder",
	// 	"audio",
	// 	"audio_bit_rate",
	// 	"audio_codec_options",
	// 	"control",
	// 	"new_display",
	// 	"start_app",
	// 	"max_size",
	// 	"log_level",
	// 	"cleanup",
	// }

	// for _, key := range keys {
	// 	if v, ok := params[key]; ok && v != "" {
	// 		args = append(args, fmt.Sprintf("%s=%s", key, v))
	// 	}
	// }

	for k := range params {
		//如果首字母大写，则跳过
		if len(k) > 0 && k[0] >= 'A' && k[0] <= 'Z' {
			continue
		}

		if v := params[k]; v != "" {
			args = append(args, fmt.Sprintf("%s=%s", k, v))
		}
	}
	return args
}

