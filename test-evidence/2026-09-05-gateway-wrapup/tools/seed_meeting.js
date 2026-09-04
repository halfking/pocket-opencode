-- E2E 种子：一条带转写分段的会议（经 CDP 在页面上下文执行）
-- 步骤 1/2：建连接并插入，返回验证 JSON
(async () => {
  const P = window.Capacitor.Plugins.CapacitorSQLite;
  const DB = 'lobster';
  let opened = false;
  try {
    const is = await P.isConnection({ database: DB, readonly: false });
    if (!is.result) {
      await P.createConnection({ database: DB, encrypted: true, mode: 'secret', version: 1, readonly: false });
      await P.open({ database: DB, readonly: false });
      opened = true;
    }
  } catch (e) {
    return 'OPEN-FAIL: ' + (e && e.message ? e.message : String(e));
  }
  const now = Date.now();
  const mid = 'meeting-e2e-gateway-' + now;
  const mk = [
    "INSERT INTO local_meetings (id,title,location,participants,audio_path,duration_ms,transcript,summary,live_summary,refined_transcript,recommendations,note_id,status,started_at,created_at,deleted_at) VALUES ('%ID%','E2E Gateway Wrapup Sync','emulator-5554','[\"Alice\",\"Bob\"]',NULL,64000,NULL,NULL,NULL,NULL,NULL,NULL,'completed',%NOW%,%NOW%,NULL)",
    "INSERT INTO local_meeting_segments (id,meeting_id,speaker_label,lang,confidence,start_ms,end_ms,text) VALUES ('%ID%-s1','%ID%','Alice','en',0.99,0,16000,'Lets kick off the AI gateway wrapup review for this cycle')",
    "INSERT INTO local_meeting_segments (id,meeting_id,speaker_label,lang,confidence,start_ms,end_ms,text) VALUES ('%ID%-s2','%ID%','Bob','en',0.98,16000,32000,'The dynamic gateway chain is healthy and kimi-k3 answers in about two seconds')",
    "INSERT INTO local_meeting_segments (id,meeting_id,speaker_label,lang,confidence,start_ms,end_ms,text) VALUES ('%ID%-s3','%ID%','Alice','en',0.98,32000,48000,'GLM and minimax still have no upstream provider configured so those calls fall back')",
    "INSERT INTO local_meeting_segments (id,meeting_id,speaker_label,lang,confidence,start_ms,end_ms,text) VALUES ('%ID%-s4','%ID%','Bob','en',0.99,48000,64000,'Action item for the maintainer is provider configuration and the test DSN rotation')"
  ].map(s => s.replace(/%ID%/g, mid).replace(/%NOW%/g, String(now)));
  try {
    await P.execute({ database: DB, statements: mk.join('\n'), transaction: true });
  } catch (e) {
    return 'INSERT-FAIL: ' + (e && e.message ? e.message : String(e));
  }
  const chk = await P.query({ database: DB, statement: "SELECT m.id, m.title, m.status, (SELECT COUNT(*) FROM local_meeting_segments s WHERE s.meeting_id = m.id) AS segs FROM local_meetings m WHERE m.id = '" + mid + "'", values: [] });
  if (opened) { try { await P.closeConnection({ database: DB, readonly: false }); } catch (e) {} }
  return JSON.stringify({ meetingId: mid, check: chk.values });
})()
