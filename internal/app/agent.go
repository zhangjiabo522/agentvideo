package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
)

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type agentRequest struct {
	Project Project `json:"project"`
	Prompt  string  `json:"prompt"`
	Scope   string  `json:"scope"`
	SceneID string  `json:"sceneId"`
	NodeID  string  `json:"nodeId"`
}

func (s *Server) agent(w http.ResponseWriter, r *http.Request) {
	var input agentRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if strings.TrimSpace(input.Prompt) == "" || len([]rune(input.Prompt)) > 6000 {
		errorJSON(w, 400, "请输入 1 至 6000 个字符的修改要求")
		return
	}
	if err := validateProject(&input.Project); err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	if err := validateScope(input); err != nil {
		errorJSON(w, 400, err.Error())
		return
	}
	settings := s.currentSettings()
	if settings.BaseURL == "" || settings.Model == "" {
		errorJSON(w, 400, "尚未配置文本模型，请在模型设置中填写服务地址和模型名称后重试")
		return
	}
	projectJSON, _ := json.Marshal(input.Project)
	system := `你是可视化视频制作 Agent，只使用文本能力生成可编辑工程，不调用视频生成模型。仅返回一个完整的 Project JSON 对象，不要 Markdown、解释、代码、注释。必须保留 id、revision、updatedAt、width、height、fps。全部时间 duration/start/end/trimStart 均以整数帧表示。Project={id,name,width,height,fps,revision,updatedAt,scenes:Scene[],audio:AudioTrack[]}。Scene={id,name,duration,background,notes,nodes:SceneNode[]}。SceneNode={id,type,name,x,y,width,height,rotation,opacity,color,text?,fontSize?,fontWeight?,src?,objectFit?,data?,labels?,start,end,animation,locked,hidden,protected?:string[]}。AudioTrack={id,name,src,start,trimStart,duration,volume}。type 仅 text/image/rect/circle/chart，animation 仅 none/fade/slide/zoom；objectFit仅cover/contain，opacity 为 0..1。每个图层 start>=0、end>start、end<=所属镜头duration。新 id 仅英文字母数字或连字符下划线且不重复。至少1镜头，最多20镜头、300图层、总时长120秒。空镜头可创作内容。没有素材时使用文字图形图表，不编造图片地址。locked=true 的已有图层不得修改、解锁、删除或移动到其他镜头。protected 列出用户手动编辑过的属性，必须保留列表，不得移除、删除所属图层、移动到其他镜头或修改这些属性；唯一例外是 scope=node 时用户直接要求修改所选 nodeId，可以调整该节点的受保护属性但保留protected列表，其他图层仍全部保留。scope=scene 时仅修改 sceneId 指定的镜头，其他镜头及工程元数据和音频必须完全保持；scope=node 时仅修改 nodeId 指定的图层，其他图层及所有镜头属性和工程元数据和音频必须保持。scope=project 时按要求修改。画面布局留合理边距，中文字号足够可读；无需指定生图才能完成视频。`
	system += "\nAudioTrack还可包含sourceDuration、fadeIn、fadeOut，均为整数帧。已有音频的这些字段必须保留，trimStart+duration不能超过sourceDuration，淡入淡出长度必须在0至duration之间。未被明确要求修改时保留用户上传音频及其时间、音量、裁剪参数。"
	user := fmt.Sprintf("修改范围 scope=%s，sceneId=%s，nodeId=%s\n用户要求：%s\n当前完整工程：%s", input.Scope, input.SceneID, input.NodeID, input.Prompt, projectJSON)
	var response chatResponse
	payload := map[string]any{"model": settings.Model, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}}, "temperature": 0.3, "stream": false}
	if err := s.providerJSON(r.Context(), endpoint(settings.BaseURL, "chat/completions"), settings.APIKey, payload, &response); err != nil {
		errorJSON(w, 502, "Agent 生成失败: "+err.Error())
		return
	}
	if len(response.Choices) == 0 {
		errorJSON(w, 502, "模型没有返回工程内容")
		return
	}
	content := strings.TrimSpace(response.Choices[0].Message.Content)
	if strings.HasPrefix(content, "```json") && strings.HasSuffix(content, "```") {
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, "```json"), "```"))
	} else if strings.HasPrefix(content, "```") && strings.HasSuffix(content, "```") {
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, "```"), "```"))
	}
	var candidate Project
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		errorJSON(w, 502, "模型返回的工程不是有效 JSON，请调整要求后重试")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		errorJSON(w, 502, "模型返回了多份工程或额外内容，已拒绝应用")
		return
	}
	if err := validateProject(&candidate); err != nil {
		errorJSON(w, 422, "模型工程校验失败: "+err.Error())
		return
	}
	if err := validateAgentChange(input, candidate); err != nil {
		errorJSON(w, 422, "模型修改超出允许范围，已拒绝应用: "+err.Error())
		return
	}
	candidate.Revision = input.Project.Revision
	candidate.UpdatedAt = input.Project.UpdatedAt
	writeJSON(w, 200, map[string]any{"project": candidate, "message": "修改已通过校验，可在画布查看并撤销"})
}

func validateScope(input agentRequest) error {
	if input.Scope == "project" {
		return nil
	}
	if input.Scope != "scene" && input.Scope != "node" {
		return errors.New("请指定工程、当前镜头或当前图层作为修改范围")
	}
	for _, scene := range input.Project.Scenes {
		if scene.ID != input.SceneID {
			continue
		}
		if input.Scope == "scene" {
			return nil
		}
		for _, node := range scene.Nodes {
			if node.ID == input.NodeID {
				if node.Locked {
					return errors.New("当前图层已锁定，请先解锁")
				}
				return nil
			}
		}
	}
	return errors.New("所选镜头或图层不存在")
}

func validateAgentChange(input agentRequest, candidate Project) error {
	original := input.Project
	if candidate.ID != original.ID || candidate.Width != original.Width || candidate.Height != original.Height || candidate.FPS != original.FPS {
		return errors.New("不得改变工程编号、画布规格或帧率")
	}
	for _, scene := range original.Scenes {
		for _, node := range scene.Nodes {
			var matching *SceneNode
			for i := range candidate.Scenes {
				if candidate.Scenes[i].ID == scene.ID {
					for j := range candidate.Scenes[i].Nodes {
						if candidate.Scenes[i].Nodes[j].ID == node.ID {
							matching = &candidate.Scenes[i].Nodes[j]
						}
					}
				}
			}
			if len(node.Protected) > 0 && matching == nil {
				return fmt.Errorf("含手动属性的图层「%s」被删除或移至其他镜头", node.Name)
			}
			if matching != nil && !slices.Equal(node.Protected, matching.Protected) {
				return fmt.Errorf("图层「%s」的手动属性保护列表被改变", node.Name)
			}
			if matching != nil && !(input.Scope == "node" && node.ID == input.NodeID && scene.ID == input.SceneID) {
				beforeJSON, _ := json.Marshal(node)
				afterJSON, _ := json.Marshal(matching)
				var before, after map[string]any
				json.Unmarshal(beforeJSON, &before)
				json.Unmarshal(afterJSON, &after)
				for _, property := range node.Protected {
					if !reflect.DeepEqual(before[property], after[property]) {
						return fmt.Errorf("图层「%s」的手动属性 %s 被改变", node.Name, property)
					}
				}
			}
			if !node.Locked {
				continue
			}
			found := false
			for _, nextScene := range candidate.Scenes {
				if nextScene.ID != scene.ID {
					continue
				}
				for _, nextNode := range nextScene.Nodes {
					if node.ID == nextNode.ID && reflect.DeepEqual(node, nextNode) {
						found = true
					}
				}
			}
			if !found {
				return fmt.Errorf("锁定图层「%s」被修改或删除", node.Name)
			}
		}
	}
	if input.Scope == "project" {
		return nil
	}
	if candidate.Name != original.Name || !reflect.DeepEqual(candidate.Audio, original.Audio) || len(candidate.Scenes) != len(original.Scenes) {
		return errors.New("局部修改不得改变工程名称、音频或镜头数量")
	}
	for i, scene := range original.Scenes {
		next := candidate.Scenes[i]
		if scene.ID != next.ID {
			return errors.New("局部修改不得改变镜头编号或顺序")
		}
		if scene.ID != input.SceneID {
			if !reflect.DeepEqual(scene, next) {
				return errors.New("修改了其他镜头")
			}
			continue
		}
		if input.Scope == "scene" {
			continue
		}
		if scene.Name != next.Name || scene.Duration != next.Duration || scene.Background != next.Background || scene.Notes != next.Notes || len(scene.Nodes) != len(next.Nodes) {
			return errors.New("图层修改不得改变镜头属性或图层数量")
		}
		for j, node := range scene.Nodes {
			if node.ID != next.Nodes[j].ID {
				return errors.New("图层修改不得改变图层编号或顺序")
			}
			if node.ID != input.NodeID && !reflect.DeepEqual(node, next.Nodes[j]) {
				return errors.New("修改了其他图层")
			}
		}
	}
	return nil
}
