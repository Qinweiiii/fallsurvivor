// Package profile 负责求职画像、简历与可复用申请信息。
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/repository"
	"github.com/eddiel/fallsurvivor/backend/internal/security"
)

// Service 是画像服务。
type Service struct {
	store *repository.Store
	llm   *ai.Client
	// storageDir 是固定的上传根目录，来自配置，绝不接受用户输入。
	storageDir  string
	maxUploadMB int64
}

// NewService 创建服务。
func NewService(store *repository.Store, llm *ai.Client, storageDir string, maxUploadMB int64) (*Service, error) {
	abs, err := filepath.Abs(storageDir)
	if err != nil {
		return nil, fmt.Errorf("解析存储目录失败: %w", err)
	}
	// 目录权限 0700，仅当前用户可访问。
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, fmt.Errorf("创建存储目录失败: %w", err)
	}
	return &Service{store: store, llm: llm, storageDir: abs, maxUploadMB: maxUploadMB}, nil
}

// GetProfile 获取求职画像，不存在时返回默认值。
func (s *Service) GetProfile(ctx context.Context, userID model.ID) (*model.JobProfile, error) {
	p, err := s.store.User.GetProfile(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return DefaultProfile(userID), nil
	}
	return p, err
}

// DefaultProfile 返回文档中约定的默认画像。
func DefaultProfile(userID model.ID) *model.JobProfile {
	return &model.JobProfile{
		UserID:             userID,
		TargetRoles:        model.JSONStringArray{"后端", "AI后端", "AI全栈", "Agent"},
		PreferredLanguages: model.JSONStringArray{"Go", "Python"},
		PreferredLocations: model.JSONStringArray{"深圳", "东莞", "广州", "北京", "上海"},
		CompanyPreferences: model.JSONStringArray{"大厂", "外企"},
		TargetIndustries:   model.JSONStringArray{},
		GraduationYear:     2027,
	}
}

// UpdateProfileInput 是画像更新入参。
type UpdateProfileInput struct {
	TargetRoles        []string
	PreferredLanguages []string
	PreferredLocations []string
	CompanyPreferences []string
	TargetIndustries   []string
	GraduationYear     int
}

// UpdateProfile 更新求职画像。
func (s *Service) UpdateProfile(ctx context.Context, userID model.ID, in UpdateProfileInput) (*model.JobProfile, error) {
	p := &model.JobProfile{
		UserID:             userID,
		TargetRoles:        in.TargetRoles,
		PreferredLanguages: in.PreferredLanguages,
		PreferredLocations: in.PreferredLocations,
		CompanyPreferences: in.CompanyPreferences,
		TargetIndustries:   in.TargetIndustries,
		GraduationYear:     in.GraduationYear,
	}
	// 复用已有主键以触发 upsert。
	if existing, err := s.store.User.GetProfile(ctx, userID); err == nil {
		p.ID = existing.ID
	}
	if err := s.store.User.UpsertProfile(ctx, p); err != nil {
		return nil, err
	}
	return s.store.User.GetProfile(ctx, userID)
}

// ---------------- 简历上传 ----------------

// 允许的简历类型。扩展名与 magic byte 都必须匹配。
var allowedResumeTypes = map[string]struct {
	mime   string
	magics [][]byte
}{
	".pdf": {
		mime:   "application/pdf",
		magics: [][]byte{{0x25, 0x50, 0x44, 0x46}}, // %PDF
	},
	".docx": {
		mime:   "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		magics: [][]byte{{0x50, 0x4B, 0x03, 0x04}}, // ZIP
	},
	".md": {
		mime:   "text/markdown",
		magics: nil, // 纯文本无固定魔数
	},
	".txt": {
		mime:   "text/plain",
		magics: nil,
	},
}

// 上传相关错误。
var (
	ErrFileTooLarge    = errors.New("简历文件超出大小限制")
	ErrUnsupportedType = errors.New("只支持 PDF / DOCX / TXT / Markdown 格式的简历")
	ErrContentMismatch = errors.New("文件内容与扩展名不匹配")
	ErrInvalidFileName = errors.New("文件名不合法")
)

// UploadResume 保存上传的简历。
//
// 安全措施：
//  1. 扩展名白名单；
//  2. magic byte 校验，防止改扩展名绕过；
//  3. 大小上限；
//  4. 服务端生成 UUID 文件名，完全丢弃用户提供的路径；
//  5. 存储目录固定来自配置，并做绝对路径边界校验；
//  6. 文件权限 0600，目录不在 web root 下且不对外提供静态访问。
func (s *Service) UploadResume(ctx context.Context, userID model.ID, header *multipart.FileHeader) (*model.Resume, error) {
	if header == nil {
		return nil, ErrInvalidFileName
	}

	maxBytes := s.maxUploadMB << 20
	if header.Size > maxBytes {
		return nil, ErrFileTooLarge
	}

	// 只取原始文件名的 base，且仅用于展示，绝不用于拼接路径。
	origName := filepath.Base(strings.ReplaceAll(header.Filename, `\`, "/"))
	if origName == "" || origName == "." || origName == ".." || strings.ContainsRune(origName, 0) {
		return nil, ErrInvalidFileName
	}

	ext := strings.ToLower(filepath.Ext(origName))
	spec, ok := allowedResumeTypes[ext]
	if !ok {
		return nil, ErrUnsupportedType
	}
	// 拒绝双扩展名，例如 resume.pdf.exe 已被上面的 Ext 拦住，
	// 这里再拒绝 resume.exe.pdf 这类可疑命名。
	if strings.Count(origName, ".") > 1 {
		inner := strings.ToLower(filepath.Ext(strings.TrimSuffix(origName, ext)))
		if inner != "" {
			if _, benign := allowedResumeTypes[inner]; !benign {
				return nil, ErrUnsupportedType
			}
		}
	}

	src, err := header.Open()
	if err != nil {
		return nil, err
	}
	defer src.Close()

	// 读取全部内容（已限制大小），用于魔数校验与文本提取。
	content, err := io.ReadAll(io.LimitReader(src, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, ErrFileTooLarge
	}
	if len(spec.magics) > 0 && !matchMagic(content, spec.magics) {
		return nil, ErrContentMismatch
	}

	// 服务端生成文件名，与用户输入完全无关。
	storedName := uuid.NewString() + ext
	fullPath := filepath.Join(s.storageDir, storedName)

	// 边界校验：即便上面已保证安全，这里仍做一次前缀检查。
	if !strings.HasPrefix(fullPath, s.storageDir+string(os.PathSeparator)) {
		return nil, ErrInvalidFileName
	}

	if err := os.WriteFile(fullPath, content, 0o600); err != nil {
		return nil, fmt.Errorf("保存简历失败: %w", err)
	}

	rec := &model.Resume{
		UserID:         userID,
		FileName:       origName,
		FilePath:       storedName, // 只存相对名，避免绝对路径入库
		FileSize:       int64(len(content)),
		MimeType:       spec.mime,
		ParseStatus:    model.ResumeParsePending,
		StructuredData: model.JSONMap{},
	}

	// 纯文本格式可以直接提取正文。
	if ext == ".txt" || ext == ".md" {
		rec.RawText = security.RedactText(string(content))
	}

	if err := s.store.Resume.Create(ctx, rec); err != nil {
		// 落库失败则清理已写入的文件。
		_ = os.Remove(fullPath)
		return nil, err
	}

	// 第一份简历自动设为当前简历。
	if list, err := s.store.Resume.List(ctx, userID); err == nil && len(list) == 1 {
		_ = s.store.Resume.SetCurrent(ctx, userID, rec.ID)
		rec.IsCurrent = true
	}

	return rec, nil
}

// matchMagic 校验文件头是否匹配任一魔数。
func matchMagic(content []byte, magics [][]byte) bool {
	for _, m := range magics {
		if len(content) >= len(m) && string(content[:len(m)]) == string(m) {
			return true
		}
	}
	return false
}

// ReadResumeText 读取简历纯文本。
//
// 说明：PDF / DOCX 的文本抽取需要额外依赖，第一版策略是
// 让用户在界面上粘贴简历文本（PUT /resumes/:id/text），
// 从而避免引入重型解析库，也避免解析失败导致体验受损。
func (s *Service) ReadResumeText(ctx context.Context, userID, resumeID model.ID) (string, error) {
	rec, err := s.store.Resume.GetByID(ctx, userID, resumeID)
	if err != nil {
		return "", err
	}
	if rec.RawText != "" {
		return rec.RawText, nil
	}
	if rec.MimeType != "text/plain" && rec.MimeType != "text/markdown" {
		return "", nil
	}

	// 只允许读取存储目录下的文件，且文件名由服务端生成。
	fullPath, err := s.resolveStoredPath(rec.FilePath)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(fullPath)
	if err != nil {
		return "", err
	}
	return security.RedactText(string(b)), nil
}

// resolveStoredPath 把库中存的文件名解析为安全的绝对路径。
func (s *Service) resolveStoredPath(stored string) (string, error) {
	// 只接受纯文件名，任何路径分隔符都视为攻击。
	if stored == "" || strings.ContainsAny(stored, `/\`) || strings.Contains(stored, "..") {
		return "", ErrInvalidFileName
	}
	full := filepath.Join(s.storageDir, filepath.Base(stored))
	clean := filepath.Clean(full)
	if !strings.HasPrefix(clean, s.storageDir+string(os.PathSeparator)) {
		return "", ErrInvalidFileName
	}
	return clean, nil
}

// SetResumeText 由用户手工提供简历文本并触发解析。
func (s *Service) SetResumeText(ctx context.Context, userID, resumeID model.ID, text string) (*model.Resume, error) {
	rec, err := s.store.Resume.GetByID(ctx, userID, resumeID)
	if err != nil {
		return nil, err
	}

	cleaned := security.RedactText(strings.TrimSpace(text))
	if cleaned == "" {
		return nil, errors.New("简历文本不能为空")
	}

	fields := map[string]any{
		"raw_text":     cleaned,
		"parse_status": model.ResumeParseRunning,
		"parse_error":  "",
	}
	if err := s.store.Resume.Update(ctx, resumeID, fields); err != nil {
		return nil, err
	}

	// 同步解析（简历不长，无需异步）。
	if s.llm.Enabled() {
		parsed, err := s.llm.ParseResume(ctx, cleaned)
		if err != nil {
			slog.Warn("简历解析失败", "resume_id", resumeID.String(), "error", err.Error())
			_ = s.store.Resume.Update(ctx, resumeID, map[string]any{
				"parse_status": model.ResumeParseFailed,
				"parse_error":  "AI 解析失败，可稍后重试",
			})
		} else {
			raw, _ := parsed.ToJSONMap()
			_ = s.store.Resume.Update(ctx, resumeID, map[string]any{
				"structured_data": raw,
				"parse_status":    model.ResumeParseCompleted,
			})
		}
	} else {
		_ = s.store.Resume.Update(ctx, resumeID, map[string]any{
			"parse_status": model.ResumeParseFailed,
			"parse_error":  "未配置 AI 能力，无法结构化解析",
		})
	}

	rec.RawText = cleaned
	return s.store.Resume.GetByID(ctx, userID, resumeID)
}

// ListResumes 查询简历列表。
func (s *Service) ListResumes(ctx context.Context, userID model.ID) ([]model.Resume, error) {
	items, err := s.store.Resume.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	// 列表不返回全文，减少传输量。
	for i := range items {
		items[i].RawText = ""
	}
	return items, nil
}

// GetResume 查询单份简历。
func (s *Service) GetResume(ctx context.Context, userID, id model.ID) (*model.Resume, error) {
	return s.store.Resume.GetByID(ctx, userID, id)
}

// SetCurrentResume 设置当前简历。
func (s *Service) SetCurrentResume(ctx context.Context, userID, id model.ID) error {
	return s.store.Resume.SetCurrent(ctx, userID, id)
}

// DeleteResume 删除简历及其文件。
func (s *Service) DeleteResume(ctx context.Context, userID, id model.ID) error {
	rec, err := s.store.Resume.GetByID(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := s.store.Resume.Delete(ctx, userID, id); err != nil {
		return err
	}
	if full, err := s.resolveStoredPath(rec.FilePath); err == nil {
		_ = os.Remove(full)
	}
	return nil
}

// ---------------- 可复用申请信息 ----------------

// ApplicationProfileData 是可复用的普通申请信息。
//
// 严格限定字段，绝不包含身份证、银行卡、密码、验证码等敏感项。
type ApplicationProfileData struct {
	Basic struct {
		Name      string `json:"name"`
		Phone     string `json:"phone"`
		Email     string `json:"email"`
		Gender    string `json:"gender"`
		Hometown  string `json:"hometown"`
		Homepage  string `json:"homepage"`
		SelfIntro string `json:"self_intro"`
	} `json:"basic"`
	Education struct {
		School         string `json:"school"`
		Major          string `json:"major"`
		Degree         string `json:"degree"`
		StartDate      string `json:"start_date"`
		GraduationDate string `json:"graduation_date"`
		GPA            string `json:"gpa"`
	} `json:"education"`
	Preference struct {
		City string `json:"city"`
		Role string `json:"role"`
	} `json:"preference"`
	Skills struct {
		Summary   string `json:"summary"`
		Languages string `json:"languages"`
	} `json:"skills"`
	Experience struct {
		Projects    string `json:"projects"`
		Internships string `json:"internships"`
		Awards      string `json:"awards"`
	} `json:"experience"`
}

// Flatten 把结构体展开为 field_mapper 使用的路径 → 值映射。
func (d *ApplicationProfileData) Flatten() map[string]string {
	if d == nil {
		return map[string]string{}
	}
	return map[string]string{
		"basic.name":                d.Basic.Name,
		"basic.phone":               d.Basic.Phone,
		"basic.email":               d.Basic.Email,
		"basic.gender":              d.Basic.Gender,
		"basic.hometown":            d.Basic.Hometown,
		"basic.homepage":            d.Basic.Homepage,
		"basic.self_intro":          d.Basic.SelfIntro,
		"education.school":          d.Education.School,
		"education.major":           d.Education.Major,
		"education.degree":          d.Education.Degree,
		"education.start_date":      d.Education.StartDate,
		"education.graduation_date": d.Education.GraduationDate,
		"education.gpa":             d.Education.GPA,
		"preference.city":           d.Preference.City,
		"preference.role":           d.Preference.Role,
		"skills.summary":            d.Skills.Summary,
		"skills.languages":          d.Skills.Languages,
		"experience.projects":       d.Experience.Projects,
		"experience.internships":    d.Experience.Internships,
		"experience.awards":         d.Experience.Awards,
	}
}

// GetApplicationProfile 获取可复用申请信息。
func (s *Service) GetApplicationProfile(ctx context.Context, userID model.ID) (*ApplicationProfileData, error) {
	rec, err := s.store.User.GetApplicationProfile(ctx, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return &ApplicationProfileData{}, nil
	}
	if err != nil {
		return nil, err
	}

	raw, err := json.Marshal(rec.ProfileData)
	if err != nil {
		return nil, err
	}
	// 反序列化到具体结构体，忽略任何多余字段。
	var out ApplicationProfileData
	if err := json.Unmarshal(raw, &out); err != nil {
		return &ApplicationProfileData{}, nil
	}
	return &out, nil
}

// UpdateApplicationProfile 更新可复用申请信息。
func (s *Service) UpdateApplicationProfile(ctx context.Context, userID model.ID, in *ApplicationProfileData) (*ApplicationProfileData, error) {
	// 只序列化白名单结构体，用户传入的多余字段会被自动丢弃。
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	m := model.JSONMap{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}

	rec := &model.ApplicationProfile{UserID: userID, ProfileData: m}
	if existing, err := s.store.User.GetApplicationProfile(ctx, userID); err == nil {
		rec.ID = existing.ID
	}
	if err := s.store.User.UpsertApplicationProfile(ctx, rec); err != nil {
		return nil, err
	}
	return in, nil
}
