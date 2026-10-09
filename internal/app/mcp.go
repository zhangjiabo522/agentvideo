package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func studioSchema(kind, description string) map[string]any {
	return map[string]any{"type": kind, "description": description}
}

func studioObject(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func studioEnum(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": description, "enum": values}
}

func studioArray(description string, items any) map[string]any {
	return map[string]any{"type": "array", "description": description, "items": items}
}

func studioNodeSchema() map[string]any {
	return studioObject(map[string]any{
		"id":   studioSchema("string", "可省略，由服务生成；自行填写需全工程唯一，仅字母数字下划线连字符"),
		"type": studioEnum("图层类型", "text", "image", "rect", "circle", "chart"),
		"name": studioSchema("string", "图层名称"),
		"x":    studioSchema("number", "左上角横坐标，像素"), "y": studioSchema("number", "左上角纵坐标，像素"),
		"width": studioSchema("number", "宽度，正数像素"), "height": studioSchema("number", "高度，正数像素"),
		"rotation": studioSchema("number", "旋转角度，默认0"), "opacity": studioSchema("number", "不透明度0至1，通常填1"),
		"color": studioSchema("string", "文字或图形颜色，如#ffffff"),
		"text":  studioSchema("string", "文本内容，仅text图层"), "fontSize": studioSchema("number", "字号像素，中文标题建议48至80"),
		"fontWeight": studioSchema("integer", "字重，如400或700"), "src": studioSchema("string", "已有图片素材地址，不得编造；可调用生图工具"),
		"objectFit": studioEnum("图片适配", "cover", "contain"),
		"data":      studioArray("柱图数字数据", studioSchema("number", "有限数字")), "labels": studioArray("柱图标签", studioSchema("string", "标签")),
		"start": studioSchema("integer", "镜头内入场帧，0至end-1"), "end": studioSchema("integer", "镜头内出场帧，必须大于start且不超过镜头duration"),
		"animation": studioEnum("入场动画", "none", "fade", "slide", "zoom"),
		"locked":    studioSchema("boolean", "默认false；已有锁定图层禁止修改"), "hidden": studioSchema("boolean", "默认false"),
	}, "type", "name", "x", "y", "width", "height", "opacity", "color", "start", "end", "animation")
}

func studioSceneSchema() map[string]any {
	return studioObject(map[string]any{
		"id":   studioSchema("string", "可省略由服务生成，全工程唯一"),
		"name": studioSchema("string", "镜头标题"), "duration": studioSchema("integer", "镜头时长，整数帧，如30fps时5秒为150"),
		"background": studioSchema("string", "背景色，如#10141c"), "notes": studioSchema("string", "分镜说明"),
		"nodes": studioArray("按由底至顶顺序排列的图层，可空数组", studioNodeSchema()),
	}, "name", "duration", "background", "nodes")
}

func studioAudioSchema() map[string]any {
	return studioObject(map[string]any{
		"id": studioSchema("string", "可省略由服务生成"), "name": studioSchema("string", "音轨标题"),
		"src": studioSchema("string", "已上传音频地址；不能编造"), "start": studioSchema("integer", "视频全局起点帧"),
		"trimStart": studioSchema("integer", "从源音频第几帧开始取，默认0"), "duration": studioSchema("integer", "截取长度，帧"),
		"sourceDuration": studioSchema("integer", "源素材总帧数，已知时必填；trimStart+duration不可超出"),
		"volume":         studioSchema("number", "音量0至2，通常1"), "fadeIn": studioSchema("integer", "淡入帧数，0至duration"), "fadeOut": studioSchema("integer", "淡出帧数，0至duration"),
	}, "name", "src", "start", "trimStart", "duration", "volume")
}

func (s *Server) studioToolDefinitions() []map[string]any {
	id := func() map[string]any {
		return studioSchema("string", "工程编号，先读取工程取得；不得使用其他工程编号")
	}
	revision := func() map[string]any {
		return studioSchema("integer", "读取工程得到的最新revision，版本冲突必须重新获取后重试")
	}
	definitions := []map[string]any{}
	add := func(name, description string, parameters map[string]any) {
		definitions = append(definitions, map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description, "parameters": parameters}})
	}
	add("video_project_list", "列出已保存的视频工程及其结构。密钥不会返回。", studioObject(map[string]any{}))
	add("video_project_get", "读取工程最新完整结构、所有镜头图层音轨与revision。剪辑前先读，所有时间以整数帧计，镜头连续拼接，图层时间相对镜头，音轨相对视频。", studioObject(map[string]any{"projectId": id()}, "projectId"))
	add("video_project_create", "新建视频工程，默认1280×720、30fps、5秒空镜头。可直接提供完整project。", studioObject(map[string]any{
		"name": studioSchema("string", "工程名"), "width": studioSchema("integer", "画布宽度64至1920偶数"), "height": studioSchema("integer", "画布高度64至1920偶数"), "fps": studioSchema("integer", "帧率1至60，通常30"),
		"project": studioObject(map[string]any{"id": studioSchema("string", "可省略自动生成"), "name": studioSchema("string", "工程名"), "width": studioSchema("integer", "偶数像素"), "height": studioSchema("integer", "偶数像素"), "fps": studioSchema("integer", "帧率"), "revision": studioSchema("integer", "创建时填0"), "updatedAt": studioSchema("string", "创建时空字符串"), "scenes": studioArray("1至20个镜头", studioSceneSchema()), "audio": studioArray("0至12个音轨", studioAudioSchema())}, "name", "width", "height", "fps", "scenes", "audio"),
	}))
	patchFields := map[string]any{}
	for key, schema := range studioNodeSchema()["properties"].(map[string]any) {
		if key != "id" && key != "locked" && key != "hidden" {
			patchFields[key] = schema
		}
	}
	patchFields["hidden"] = studioSchema("boolean", "隐藏图层")
	for key, schema := range studioAudioSchema()["properties"].(map[string]any) {
		if key != "id" {
			patchFields[key] = schema
		}
	}
	for key, schema := range studioSceneSchema()["properties"].(map[string]any) {
		if key != "id" && key != "nodes" {
			patchFields[key] = schema
		}
	}
	patchFields["fps"] = studioSchema("integer", "工程帧率")
	operation := studioObject(map[string]any{
		"type":    studioEnum("scene.add需scene；scene.update需sceneId+patch；scene.delete需sceneId；scene.move需sceneId+index；node.add需sceneId+node；node.update需sceneId+nodeId+patch；node.delete需sceneId+nodeId；audio.add需audio；audio.update需audioId+patch；audio.delete需audioId；project.update需patch", "scene.add", "scene.update", "scene.delete", "scene.move", "node.add", "node.update", "node.delete", "audio.add", "audio.update", "audio.delete", "project.update"),
		"sceneId": studioSchema("string", "目标已有镜头编号"), "nodeId": studioSchema("string", "目标已有图层编号"), "audioId": studioSchema("string", "目标已有音轨编号"),
		"index": studioSchema("integer", "从0开始的插入/移动后位置；scene.move必填"),
		"scene": studioSceneSchema(), "node": studioNodeSchema(), "audio": studioAudioSchema(),
		"patch": studioObject(patchFields),
	}, "type")
	add("video_edit", "批量原子剪辑，所有操作通过校验才保存；失败完全不改变工程。用scene.move调序，scene.update patch.duration裁剪镜头，node.update patch.start/end移动裁剪图层，audio.update patch.start/trimStart/duration移动裁剪音频。禁止改锁定图层或protected属性；缩短镜头会裁剪超出图层，受保护时拒绝。音轨必须完整处于视频范围内，最多120秒20镜头300图层12音轨。", studioObject(map[string]any{"projectId": id(), "revision": revision(), "operations": studioArray("1至100个顺序操作，先扩镜头再加入长音轨", operation)}, "projectId", "revision", "operations"))
	add("video_undo", "撤销最近一次AI剪辑批次。仅当前服务运行期间保存最近50步，并保留用户锁定和属性保护。", studioObject(map[string]any{"projectId": id(), "revision": revision()}, "projectId", "revision"))
	add("video_speech_generate", "使用已保存的语音服务真正合成旁白并自动加入音轨，默认使用用户已配置的服务、模型和音色。若旁白较长自动延长末镜头以保证完整播放。远程服务失败会返回原因，不要虚构已生成。", studioObject(map[string]any{
		"projectId": id(), "text": studioSchema("string", "旁白文案1至12000字，中文宣传片需简洁自然"), "provider": studioEnum("可省略使用已保存服务，远程必须与设置匹配", "local", "mimo", "openai"),
		"voice": studioSchema("string", "可省略沿用音色；MiMo可用mimo_default、冰糖、茉莉、苏打、白桦、Mia、Chloe、Milo、Dean"), "speed": studioSchema("number", "语速0.5至2，省略沿用设置"),
		"model": studioSchema("string", "省略沿用设置，MiMo为mimo-v2.5-tts或mimo-v2.5-tts-voicedesign"), "instructions": studioSchema("string", "语气和音色描述；MiMo音色设计必须填"), "start": studioSchema("integer", "旁白全局起点帧，默认0"),
	}, "projectId", "text"))
	add("video_image_generate", "调用已保存的生图服务，真正生成图片并作为最底层加入指定镜头；省略sceneId加入首镜头。返回素材地址和新图层编号。", studioObject(map[string]any{"projectId": id(), "prompt": studioSchema("string", "图片描述1至4000字"), "sceneId": studioSchema("string", "目标镜头编号")}, "projectId", "prompt"))
	add("video_preview", "让人的实时画布跳至指定帧，并真实渲染该帧PNG截图，返回screenshot.url与本机path和工程结构。用于阶段性检查，操作通过事件推送即时可见。纯文本模型使用结构和截图链接，视觉MCP客户端可includeImage=true取得图片内容。", studioObject(map[string]any{"projectId": id(), "frame": studioSchema("integer", "全局预览帧，0至总帧数减1"), "includeImage": studioSchema("boolean", "默认false；仅MCP调用时附PNG图片内容，供可视觉模型阅读")}, "projectId", "frame"))
	add("video_export", "把最新已保存工程提交为真实MP4导出任务，返回job.id；导出有队列，用video_export_status查结果。", studioObject(map[string]any{"projectId": id()}, "projectId"))
	add("video_export_status", "查询导出任务状态、0至1进度、错误或完成后的下载url。未completed不得声称视频已输出。", studioObject(map[string]any{"jobId": studioSchema("string", "video_export返回的job.id")}, "jobId"))
	return definitions
}

func (s *Server) studioMCP(w http.ResponseWriter, r *http.Request) {
	if !studioMCPOriginAllowed(r) {
		errorJSON(w, 403, "MCP 仅允许本机同源工具连接")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	decoder := json.NewDecoder(r.Body)
	if decoder.Decode(&request) != nil || request.JSONRPC != "2.0" || request.Method == "" {
		writeJSON(w, 400, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "JSON-RPC 请求格式无效"}})
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeJSON(w, 400, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "请求只能包含一份 JSON-RPC 数据"}})
		return
	}
	if len(request.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var id any
	_ = json.Unmarshal(request.ID, &id)
	respond := func(result any) { writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": id, "result": result}) }
	failure := func(code int, message string) {
		writeJSON(w, 200, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
	}
	version := r.Header.Get("MCP-Protocol-Version")
	if version != "" && version != "2024-11-05" && version != "2025-03-26" && version != "2025-06-18" && version != "2025-11-25" {
		errorJSON(w, 400, "MCP 协议版本不支持")
		return
	}
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(request.Params, &params)
		negotiated := "2025-06-18"
		for _, supported := range []string{"2024-11-05", "2025-03-26", "2025-06-18", "2025-11-25"} {
			if params.ProtocolVersion == supported {
				negotiated = supported
			}
		}
		respond(map[string]any{"protocolVersion": negotiated, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "yingxu-video-studio", "version": "0.2.0"}, "instructions": "映序是AI可操作的视频工作台。先读取工程，再通过工具分步编排镜头、图层、旁白与导出；所有变更实时展示给人。所有时间使用整数帧，禁止编造素材。"})
	case "ping":
		respond(map[string]any{})
	case "tools/list":
		definitions := s.studioToolDefinitions()
		tools := make([]map[string]any, 0, len(definitions))
		for _, definition := range definitions {
			function := definition["function"].(map[string]any)
			tools = append(tools, map[string]any{"name": function["name"], "description": function["description"], "inputSchema": function["parameters"]})
		}
		respond(map[string]any{"tools": tools})
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Meta      json.RawMessage `json:"_meta,omitempty"`
		}
		if err := studioDecode(request.Params, &params); err != nil {
			failure(-32602, err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), studioOriginKey{}, r.Host)
		result, err := s.executeStudioTool(ctx, params.Name, params.Arguments)
		if err != nil {
			respond(map[string]any{"content": []map[string]string{{"type": "text", "text": err.Error()}}, "isError": true})
			return
		}
		data, _ := json.Marshal(result)
		content := []map[string]any{{"type": "text", "text": string(data)}}
		if params.Name == "video_preview" {
			var options struct {
				IncludeImage bool `json:"includeImage"`
			}
			_ = json.Unmarshal(params.Arguments, &options)
			if options.IncludeImage {
				if screenshot, ok := result["screenshot"].(map[string]any); ok {
					if path, ok := screenshot["path"].(string); ok {
						if imageData, err := os.ReadFile(path); err == nil {
							content = append(content, map[string]any{"type": "image", "data": base64.StdEncoding.EncodeToString(imageData), "mimeType": "image/png"})
						}
					}
				}
			}
		}
		respond(map[string]any{"content": content, "structuredContent": result, "isError": false})
	default:
		failure(-32601, "不支持的 MCP 方法: "+request.Method)
	}
}

func studioMCPOriginAllowed(r *http.Request) bool {
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || !strings.EqualFold(parsed.Host, r.Host) {
			return false
		}
	}
	return true
}
