-- 导出 29 条未净化 snippet 的原文，供 Go 侧用**当前** DeriveSnippet 重放。
--
-- 为什么导出而不是在 SQL 里判断：SQL 只能告诉你「这行看着像 QP 残留」，
-- 判不出「当前净化器跑完会得到什么」。真正的判据是拿当前代码跑一遍。
--
-- \copy 而不是 SELECT：psql 客户端在这些行的字节上会做编码转换，
-- 走 \copy 直出原始字节，避免把「客户端编码问题」误看成「数据变了」。
--
-- ⚠️ 两处踩过的坑，都留在这里免得再踩：
--   ① \copy 走的是**新连接**，不会继承本进程前面 SET 过的 search_path。
--      漏掉下面那句时它去查 public.emails（存在但空）⇒ COPY 0，
--      与「库里一条都没有」长得一模一样，却不报错。
--   ② 分隔符**不能用 E'\t'**：实测这台 psql 输出的是字面两字符 `\t`，
--      文件里 0 个制表符、29 个换行 —— Go 侧按 "\t" 切会切不开，
--      于是「解析出 0 条」，看起来像库干净了。
--      改用 chr(31)（ASCII Unit Separator）：邮件正文里不可能出现，且无转义歧义。
SET search_path TO opencode_pocket;
\copy (SELECT id || chr(31) || account_id || chr(31) || replace(replace(snippet, chr(10), ' '), chr(13), ' ') FROM emails WHERE (length(snippet) > 200 AND snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})') OR snippet ILIKE '%Content-Type%' OR snippet ILIKE '%boundary%' OR snippet ILIKE '%multipart%' ORDER BY created_at) TO 'scripts/sql/unsanitized-snippets.tsv'
