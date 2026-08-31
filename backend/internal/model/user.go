package model

import "time"

// UserProfile 是系统用户。第一版单用户，无 RBAC。
type UserProfile struct {
	ID    ID     `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	Name  string `json:"name" gorm:"column:name"`
	Email string `json:"email" gorm:"column:email"`
	Phone string `json:"phone" gorm:"column:phone"`
	Timestamps
}

// TableName 指定表名。
func (UserProfile) TableName() string { return "user_profiles" }

// JobProfile 是用户的求职画像，数组字段顺序即优先级。
type JobProfile struct {
	ID                 ID              `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID             ID              `json:"user_id" gorm:"column:user_id;type:uuid"`
	TargetRoles        JSONStringArray `json:"target_roles" gorm:"column:target_roles;type:jsonb"`
	PreferredLanguages JSONStringArray `json:"preferred_languages" gorm:"column:preferred_languages;type:jsonb"`
	PreferredLocations JSONStringArray `json:"preferred_locations" gorm:"column:preferred_locations;type:jsonb"`
	CompanyPreferences JSONStringArray `json:"company_preferences" gorm:"column:company_preferences;type:jsonb"`
	TargetIndustries   JSONStringArray `json:"target_industries" gorm:"column:target_industries;type:jsonb"`
	GraduationYear     int             `json:"graduation_year" gorm:"column:graduation_year"`
	Timestamps
}

// TableName 指定表名。
func (JobProfile) TableName() string { return "job_profiles" }

// 简历解析状态。
const (
	ResumeParsePending   = "PENDING"
	ResumeParseRunning   = "RUNNING"
	ResumeParseCompleted = "COMPLETED"
	ResumeParseFailed    = "FAILED"
)

// Resume 是上传的简历及其结构化结果。
type Resume struct {
	ID       ID     `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID   ID     `json:"user_id" gorm:"column:user_id;type:uuid"`
	FileName string `json:"file_name" gorm:"column:file_name"`
	// FilePath 是服务端生成的相对路径，不回传给前端。
	FilePath       string  `json:"-" gorm:"column:file_path"`
	FileSize       int64   `json:"file_size" gorm:"column:file_size"`
	MimeType       string  `json:"mime_type" gorm:"column:mime_type"`
	RawText        string  `json:"raw_text,omitempty" gorm:"column:raw_text"`
	StructuredData JSONMap `json:"structured_data" gorm:"column:structured_data;type:jsonb"`
	ParseStatus    string  `json:"parse_status" gorm:"column:parse_status"`
	ParseError     string  `json:"parse_error" gorm:"column:parse_error"`
	IsCurrent      bool    `json:"is_current" gorm:"column:is_current"`
	Timestamps
}

// TableName 指定表名。
func (Resume) TableName() string { return "resumes" }

// ApplicationProfile 保存可复用的普通申请信息（不含任何敏感项）。
type ApplicationProfile struct {
	ID          ID      `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID      ID      `json:"user_id" gorm:"column:user_id;type:uuid"`
	ProfileData JSONMap `json:"profile_data" gorm:"column:profile_data;type:jsonb"`
	Timestamps
}

// TableName 指定表名。
func (ApplicationProfile) TableName() string { return "application_profiles" }

// Interview 是面试记录。
type Interview struct {
	ID            ID         `json:"id" gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	ApplicationID ID         `json:"application_id" gorm:"column:application_id;type:uuid"`
	Round         string     `json:"round" gorm:"column:round"`
	ScheduledAt   *time.Time `json:"scheduled_at" gorm:"column:scheduled_at"`
	Format        string     `json:"format" gorm:"column:format"`
	Status        string     `json:"status" gorm:"column:status"`
	Notes         string     `json:"notes" gorm:"column:notes"`
	Timestamps
}

// TableName 指定表名。
func (Interview) TableName() string { return "interviews" }
