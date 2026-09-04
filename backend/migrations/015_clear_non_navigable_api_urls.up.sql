-- 旧实现将列表 API 加 #job_id=<id> 作为岗位身份，同时错误暴露成用户外链。
-- 保留 normalized_url 用于去重，仅清空这类明确由系统生成的不可访问展示 URL。
UPDATE jobs
   SET source_url = '',
       official_url = ''
 WHERE source_url = official_url
   AND source_url ~ '#(job_id|jobid|position_id|positionid|post_id|postid)=';
