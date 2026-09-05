// E2E 种子（budget90 实例真机轮）：一条带转写分段的会议 — 逐条 execute（§22.4/§24.3 坑：批量 execute 首条后静默中止）
(async () => {
  const P = window.Capacitor.Plugins.CapacitorSQLite;
  const DB = 'lobster';
  let opened = false;
  try {
    // §22.4 坑一：原生桥无 isConnection（JS 包装层方法）——直接 createConnection+open
    await P.createConnection({ database: DB, encrypted: true, mode: 'secret', version: 1, readonly: false });
    await P.open({ database: DB, readonly: false });
    opened = true;
  } catch (e) {
    // 连接已存在（前次探针/应用自建）——复用既有连接继续
    if (!/already exists/i.test(String(e && e.message))) {
      return 'OPEN-FAIL: ' + (e && e.message ? e.message : String(e));
    }
  }
  const now = Date.now();
  const mid = 'meeting-e2e-budget90-' + now;
  const mk = [
    "INSERT INTO local_meetings (id,title,location,participants,audio_path,duration_ms,transcript,summary,live_summary,refined_transcript,recommendations,note_id,status,started_at,created_at,deleted_at) VALUES ('%ID%','E2E Budget90 实例纪要复验','emulator-5554','[\"Alice\",\"Bob\"]',NULL,64000,NULL,NULL,NULL,NULL,NULL,NULL,'completed',%NOW%,%NOW%,NULL)",
    "INSERT INTO local_meeting_segments (id,meeting_id,speaker_label,lang,confidence,start_ms,end_ms,text) VALUES ('%ID%-s1','%ID%','Alice','en',0.99,0,16000,'Kick off the budget ninety instance verification for meeting summaries')",
    "INSERT INTO local_meeting_segments (id,meeting_id,speaker_label,lang,confidence,start_ms,end_ms,text) VALUES ('%ID%-s2','%ID%','Bob','en',0.98,16000,32000,'The final candidate now has no twenty second attempt window and the whole chain budget is ninety seconds')",
    "INSERT INTO local_meeting_segments (id,meeting_id,speaker_label,lang,confidence,start_ms,end_ms,text) VALUES ('%ID%-s3','%ID%','Alice','en',0.98,32000,48000,'Embed provider is still missing upstream so vector storage stays degraded gracefully')",
    "INSERT INTO local_meeting_segments (id,meeting_id,speaker_label,lang,confidence,start_ms,end_ms,text) VALUES ('%ID%-s4','%ID%','Bob','en',0.99,48000,64000,'Action item is to verify the summary generation lands directly in fast phase on this instance')"
  ].map(s => s.replace(/%ID%/g, mid).replace(/%NOW%/g, String(now)));
  const results = [];
  try {
    // §22.4 坑二：批量 execute 首条后静默中止——逐条 run
    for (const st of mk) {
      const r = await P.run({ database: DB, statement: st, values: [] });
      results.push(r && r.changes != null ? (r.changes && r.changes.changes != null ? r.changes.changes : r.changes) : 'ok');
    }
  } catch (e) {
    return 'INSERT-FAIL: ' + (e && e.message ? e.message : String(e));
  }
  const chk = await P.query({ database: DB, statement: "SELECT m.id, m.title, m.status, (SELECT COUNT(*) FROM local_meeting_segments s WHERE s.meeting_id = m.id) AS segs FROM local_meetings m WHERE m.id = '" + mid + "'", values: [] });
  if (opened) { try { await P.closeConnection({ database: DB, readonly: false }); } catch (e) {} }
  return JSON.stringify({ meetingId: mid, executed: results.length, check: chk.values });
})()
