/**
 * 敏感字段守卫。
 *
 * 这是 Worker 侧的最后一道防线：即使后端因为任何原因把敏感字段
 * 放进了填写列表，本模块也会拒绝写入。
 *
 * 关键词清单与后端 internal/security/sensitive.go 保持同步。
 * 修改时必须同时修改两处。
 */

const SENSITIVE_KEYWORDS: readonly string[] = [
  // 证件类
  '身份证', '身份証', '证件号', '证件号码', 'idcard', 'id card', 'id_card', 'id number',
  '护照', 'passport', '港澳通行证', '台胞证', '军官证', '社保卡', '社会保障号',
  'ssn', 'social security',
  // 金融类
  '银行卡', '银行账号', '银行帐号', '开户行', '卡号', 'bank card', 'bank account',
  'credit card', '信用卡', '支付', 'payment', 'iban', 'cvv',
  // 凭据类
  '密码', '口令', 'password', 'passwd', 'pwd', 'secret', 'token', 'api key', 'apikey',
  // 验证类
  '验证码', '校验码', '动态码', 'captcha', 'verification code', 'verify code',
  '短信验证', 'sms code', '邮箱验证', 'email code', 'otp', 'one-time',
  'mfa', '二次验证', '双因素', 'two factor', '2fa', 'authenticator',
  // 生物特征
  '人脸', '刷脸', 'face id', 'face recognition', 'facial', '指纹', 'fingerprint',
  '活体', 'liveness', '声纹', '虹膜',
  // 高度隐私
  '民族', '宗教', '政治面貌', '党派', '婚姻状况', '生育', '病史', '残疾',
  '犯罪记录', '征信',
];

/** 判断若干文本片段中是否含有敏感关键词。 */
export function isSensitiveText(...parts: (string | undefined | null)[]): boolean {
  for (const part of parts) {
    if (!part) continue;
    const lower = part.toLowerCase().trim();
    if (!lower) continue;
    for (const kw of SENSITIVE_KEYWORDS) {
      if (lower.includes(kw)) return true;
    }
  }
  return false;
}

/** 判断 input type 是否必须跳过。 */
export function isSensitiveInputType(type: string | undefined | null): boolean {
  if (!type) return false;
  return type.toLowerCase().trim() === 'password';
}

/**
 * 判断一个字段是否允许自动填写。
 * 任何疑似敏感的字段都返回 false。
 */
export function isFillableField(field: {
  label?: string;
  name?: string;
  placeholder?: string;
  type?: string;
}): boolean {
  if (isSensitiveInputType(field.type)) return false;
  return !isSensitiveText(field.label, field.name, field.placeholder);
}

/**
 * 提交类按钮的判定关键词。
 *
 * Worker 只用它来「识别并避开」提交按钮，绝不用于点击。
 * 系统在任何代码路径下都不会点击这些按钮，最终提交必须由用户亲自完成。
 */
const SUBMIT_KEYWORDS: readonly string[] = [
  '提交', '投递', '申请', '确认提交', '立即投递', '完成投递',
  'submit', 'apply', 'send application', 'confirm',
];

/** 判断文本是否像提交按钮。 */
export function looksLikeSubmit(text: string | undefined | null): boolean {
  if (!text) return false;
  const lower = text.toLowerCase().trim();
  return SUBMIT_KEYWORDS.some((kw) => lower.includes(kw));
}

/**
 * 判断一串数字是否可能是真实银行卡号（Luhn 校验）。
 *
 * 为什么需要它：早先的卡号规则是「13-19 位连续数字」，会把站点自己的
 * 业务 ID 一并抹掉。实测快手 recruitSubProjectCodes=20271779425607
 * 被替换成 [REDACTED_CARD]，而这个 ID 正是拼详情页 URL 的关键——
 * 过度脱敏直接破坏了采集能力。
 *
 * Luhn 是所有主流卡组织的强制校验规则，随机业务 ID 通过它的概率约 1/10。
 * 用它做二次确认，能在几乎不牺牲安全性的前提下大幅降低误伤。
 */
function looksLikeCardNumber(digits: string): boolean {
  const n = digits.length;
  if (n < 13 || n > 19) return false;

  let sum = 0;
  let double = false;
  for (let i = n - 1; i >= 0; i--) {
    let d = digits.charCodeAt(i) - 48;
    if (double) {
      d *= 2;
      if (d > 9) d -= 9;
    }
    sum += d;
    double = !double;
  }
  return sum % 10 === 0;
}

/**
 * 用于日志与对外输出的脱敏函数：抹掉证件号、卡号、Bearer、Cookie。
 *
 * 卡号采用「格式匹配 + Luhn 校验」两步判定，避免把业务 ID 当卡号抹掉。
 * 证件号（18 位身份证 / 15 位旧证）保持严格抹除——
 * 它的格式特征足够明确，误伤风险低，且泄漏后果更严重。
 */
export function redact(text: string): string {
  return text
    .replace(/\b\d{17}[\dXx]\b|\b\d{15}\b/g, '[REDACTED_ID]')
    .replace(/\b(?:\d[ -]?){12,18}\d\b/g, (match) => {
      // 去掉分隔符后再校验；只有真正通过 Luhn 的才视为卡号。
      const digits = match.replace(/[ -]/g, '');
      return looksLikeCardNumber(digits) ? '[REDACTED_CARD]' : match;
    })
    .replace(/bearer\s+[A-Za-z0-9._-]+/gi, '[REDACTED_TOKEN]')
    .replace(/(cookie|set-cookie|session[_-]?id)\s*[:=]\s*[^\s;,]+/gi, '[REDACTED_COOKIE]');
}
