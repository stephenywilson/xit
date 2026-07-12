// Static HTML for the XiT metrics dashboard (GET /dashboard).
//
// Self-contained: no external JS/CSS, no framework. It fetches the aggregate
// JSON from /api/dashboard?range=... and renders cards, an adapter breakdown,
// version distributions, simple inline-SVG trend charts, and external (public)
// download/install counters.
//
// Privacy: this page can only ever render the aggregate payload the API
// returns. It never references a raw identifier column (anonymous_install_id,
// channel_id, run_id, turn_id, event_id, ...). The footer states exactly what
// XiT does not collect. dashboard.test.js asserts no forbidden token appears in
// this markup.

export const DASHBOARD_HTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<meta name="robots" content="noindex" />
<title>XiT · Usage Dashboard</title>
<style>
  :root{
    --bg:#0d0d0f; --panel:#151518; --panel2:#1b1b1f; --line:#2a2a30;
    --fg:#f4f4f5; --muted:#9a9aa3; --faint:#6c6c75; --accent:#e8e8ea;
  }
  *{box-sizing:border-box}
  html,body{margin:0;padding:0}
  body{
    background:var(--bg); color:var(--fg);
    font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
    -webkit-font-smoothing:antialiased;
  }
  .wrap{max-width:1100px;margin:0 auto;padding:32px 20px 64px}
  header{display:flex;align-items:baseline;justify-content:space-between;flex-wrap:wrap;gap:12px;margin-bottom:4px}
  h1{font-size:20px;font-weight:600;letter-spacing:.2px;margin:0}
  h1 .sub{color:var(--faint);font-weight:400;margin-left:8px;font-size:13px}
  .meta{color:var(--faint);font-size:12px}
  h2{font-size:13px;font-weight:600;text-transform:uppercase;letter-spacing:.08em;color:var(--muted);margin:36px 0 12px}
  .ranges{display:flex;gap:6px;flex-wrap:wrap;margin:20px 0 4px}
  .ranges button{
    background:var(--panel);color:var(--muted);border:1px solid var(--line);
    border-radius:6px;padding:6px 12px;font-size:12px;cursor:pointer;letter-spacing:.02em;
  }
  .ranges button:hover{color:var(--fg);border-color:#3a3a42}
  .ranges button[aria-pressed="true"]{background:var(--accent);color:#0d0d0f;border-color:var(--accent);font-weight:600}
  .cards{display:grid;grid-template-columns:repeat(auto-fill,minmax(168px,1fr));gap:12px}
  .card{background:var(--panel);border:1px solid var(--line);border-radius:10px;padding:16px}
  .card .k{color:var(--faint);font-size:11px;text-transform:uppercase;letter-spacing:.07em}
  .card .v{font-size:24px;font-weight:600;margin-top:6px;font-variant-numeric:tabular-nums}
  .card .v small{font-size:13px;color:var(--muted);font-weight:400}
  table{width:100%;border-collapse:collapse;font-variant-numeric:tabular-nums}
  th,td{text-align:right;padding:9px 10px;border-bottom:1px solid var(--line);font-size:13px}
  th:first-child,td:first-child{text-align:left}
  th{color:var(--muted);font-weight:500;font-size:11px;text-transform:uppercase;letter-spacing:.06em}
  td.name{color:var(--fg)}
  .bar{height:5px;background:var(--panel2);border-radius:3px;overflow:hidden;margin-top:5px}
  .bar > i{display:block;height:100%;background:var(--accent)}
  .panel{background:var(--panel);border:1px solid var(--line);border-radius:10px;padding:16px}
  .grid2{display:grid;grid-template-columns:1fr 1fr;gap:16px}
  .chart{width:100%;height:160px;display:block}
  .chart .axis{stroke:var(--line);stroke-width:1}
  .chart .line{fill:none;stroke:var(--accent);stroke-width:1.6}
  .chart .area{fill:rgba(232,232,234,.06)}
  .legend{display:flex;gap:14px;flex-wrap:wrap;margin-top:8px;color:var(--muted);font-size:12px}
  .legend i{display:inline-block;width:10px;height:10px;border-radius:2px;margin-right:5px;vertical-align:middle}
  .empty{color:var(--faint);font-size:13px;padding:10px 0}
  footer{margin-top:48px;border-top:1px solid var(--line);padding-top:18px;color:var(--faint);font-size:12px;line-height:1.7}
  footer p{margin:0 0 8px}
  a{color:var(--muted)}
  @media(max-width:680px){.grid2{grid-template-columns:1fr}}
</style>
</head>
<body>
<div class="wrap">
  <header>
    <h1>XiT <span class="sub">Anonymous Usage Dashboard</span></h1>
    <div class="meta" id="meta">loading…</div>
  </header>

  <div class="meta" id="cutover" style="margin-top:2px"></div>

  <div class="ranges" id="ranges">
    <button data-range="1d">Today</button>
    <button data-range="7d">7D</button>
    <button data-range="30d" aria-pressed="true">30D</button>
    <button data-range="180d">180D</button>
    <button data-range="365d">365D</button>
    <button data-range="all">All</button>
  </div>

  <h2>Overview</h2>
  <div class="cards" id="cards"></div>

  <h2>By AI Adapter</h2>
  <div class="panel"><div id="adapters"></div></div>

  <h2>By Surface</h2>
  <div class="panel"><div id="surfaces"></div></div>

  <h2>Trends</h2>
  <div class="grid2">
    <div class="panel"><div class="k" style="color:var(--faint);font-size:11px;text-transform:uppercase;letter-spacing:.07em">Daily runs</div><svg class="chart" id="chart-runs"></svg></div>
    <div class="panel"><div class="k" style="color:var(--faint);font-size:11px;text-transform:uppercase;letter-spacing:.07em">Daily saved tokens</div><svg class="chart" id="chart-tokens"></svg></div>
  </div>
  <div class="panel" style="margin-top:16px">
    <div class="k" style="color:var(--faint);font-size:11px;text-transform:uppercase;letter-spacing:.07em">Runs by adapter over time</div>
    <svg class="chart" id="chart-adapters" style="height:200px"></svg>
    <div class="legend" id="legend-adapters"></div>
  </div>
  <div class="panel" style="margin-top:16px">
    <div class="k" style="color:var(--faint);font-size:11px;text-transform:uppercase;letter-spacing:.07em">Runs by surface over time</div>
    <svg class="chart" id="chart-surfaces" style="height:200px"></svg>
    <div class="legend" id="legend-surfaces"></div>
  </div>

  <h2>Versions</h2>
  <div class="grid2">
    <div class="panel"><div id="cli-versions"></div></div>
    <div class="panel"><div id="vscode-versions"></div></div>
  </div>

  <h2>External (public counters)</h2>
  <div class="grid2">
    <div class="panel"><div id="npm"></div></div>
    <div class="panel"><div id="vscode-installs"></div></div>
  </div>

  <footer>
    <p>XiT dashboard shows aggregate anonymous usage metrics only. No prompts, AI replies, raw terminal output, commands, file paths, repo names, usernames, emails, API keys, tokens, or full session IDs are collected or displayed.</p>
    <p>此页面只展示匿名聚合数据。XiT 不收集或展示 prompt、AI 回复、终端原始输出、命令文本、文件路径、仓库名、用户名、邮箱、API key、token 或完整 session ID。</p>
    <p style="color:var(--line)">npm downloads are public download counts (not user counts). VS Code Marketplace installs are public cumulative installs (not active users). "Active installs" approximates real usage via distinct anonymous installs and never exposes any single install id.</p>
  </footer>
</div>

<script>
const ADAPTER_LABELS = {
  claude:"Claude Code", codex:"Codex", opencode:"OpenCode",
  kimi:"Kimi", cursor:"Cursor", vscode:"VS Code", unknown:"Unknown",
};
const ADAPTER_COLORS = ["#f4f4f5","#b9b9c2","#86868f","#5d5d66","#3f3f47","#2a2a30","#71717a"];
// codex_cli/codex_ide/chatgpt_desktop_codex/codex_shared are the finer-grained
// Codex front-end breakdown added in 0.2.51; cli/hook/vscode/bridge are the
// original generic surfaces used by every other adapter.
const SURFACE_LABELS = {
  chatgpt_desktop_codex:"ChatGPT Desktop · Codex", codex_cli:"Codex CLI", codex_ide:"Codex IDE",
  codex_shared:"Codex (unspecified)", vscode:"VS Code", cli:"CLI", hook:"Hook", bridge:"Bridge",
  unknown:"Unknown",
};
const SURFACE_COLORS = ["#f4f4f5","#b9b9c2","#86868f","#5d5d66","#3f3f47","#2a2a30","#71717a","#9a9aa2"];

const nf = new Intl.NumberFormat("en-US");
function fmt(n){ return nf.format(Math.round(Number(n)||0)); }
function pct(n){ return (Number(n)||0).toFixed(0)+"%"; }
function bytes(n){
  n = Number(n)||0; const u=["B","KB","MB","GB","TB"]; let i=0;
  while(n>=1024 && i<u.length-1){ n/=1024; i++; }
  return (i===0?n:n.toFixed(1))+" "+u[i];
}
function el(tag, attrs, html){
  const e=document.createElement(tag);
  if(attrs) for(const k in attrs) e.setAttribute(k, attrs[k]);
  if(html!=null) e.innerHTML=html;
  return e;
}

let current = "30d";

async function load(range){
  current = range;
  document.querySelectorAll("#ranges button").forEach(b=>{
    b.setAttribute("aria-pressed", String(b.dataset.range===range));
  });
  document.getElementById("meta").textContent = "loading…";
  let data;
  try{
    const res = await fetch("/api/dashboard?range="+encodeURIComponent(range), {headers:{accept:"application/json"}});
    if(!res.ok) throw new Error("HTTP "+res.status);
    data = await res.json();
  }catch(err){
    document.getElementById("meta").textContent = "failed to load ("+err.message+")";
    return;
  }
  render(data);
}

function render(d){
  const s = d.summary || {};
  document.getElementById("meta").textContent =
    "range " + (d.range||current) + " · updated " + new Date(d.generated_at||Date.now()).toLocaleString();

  // Public-metrics cutover line.
  const cut = document.getElementById("cutover");
  if(d.public_start_at){
    const t = new Date(d.public_start_at);
    const p = n => String(n).padStart(2,"0");
    cut.textContent = "Public metrics since "
      + t.getUTCFullYear()+"-"+p(t.getUTCMonth()+1)+"-"+p(t.getUTCDate())
      +" "+p(t.getUTCHours())+":"+p(t.getUTCMinutes())+" UTC";
  }else{
    cut.textContent = "Public metrics since configured cutover — showing all recorded events";
  }

  // cards
  const installs = (s.active_installs==null) ? "N/A" : fmt(s.active_installs);
  const cards = [
    ["Total Runs", fmt(s.total_runs)],
    ["Total Saved Tokens", fmt(s.total_saved_tokens)],
    ["Total Saved Bytes", bytes(s.total_saved_bytes)],
    ["Avg Compression", pct((Number(s.avg_compression_ratio)||0)*100)],
    ["Success Rate", pct((Number(s.success_rate)||0)*100)],
    ["Anon. Active Installs", installs],
  ];
  const cwrap = document.getElementById("cards"); cwrap.innerHTML="";
  for(const [k,v] of cards){
    cwrap.appendChild(el("div",{class:"card"}, '<div class="k">'+k+'</div><div class="v">'+v+'</div>'));
  }

  // adapters table
  renderAdapters(d.by_adapter||[]);

  // surfaces table
  renderSurfaces(d.by_surface||[]);

  // trends
  const trend = d.daily_trend||[];
  lineChart("chart-runs", trend.map(r=>({x:r.day,y:Number(r.runs)||0})));
  lineChart("chart-tokens", trend.map(r=>({x:r.day,y:Number(r.saved_tokens)||0})));
  multiAdapterChart(d.adapter_daily_trend||[]);
  multiSurfaceChart(d.surface_daily_trend||[]);

  // versions
  renderVersions("cli-versions","CLI version","cli_version", d.by_cli_version||[]);
  renderVersions("vscode-versions","VS Code version","vscode_version", d.by_vscode_version||[]);

  // external
  renderExternal(d.external||{});
}

function renderAdapters(rows){
  const host = document.getElementById("adapters");
  if(!rows.length){ host.innerHTML='<div class="empty">No data for this range.</div>'; return; }
  const totalRuns = rows.reduce((a,r)=>a+(Number(r.runs)||0),0) || 1;
  let html = '<table><thead><tr><th>Adapter</th><th>Runs</th><th>Saved tokens</th><th>Saved bytes</th><th>Success</th><th>Share</th></tr></thead><tbody>';
  for(const r of rows){
    const runs = Number(r.runs)||0;
    const succ = Number(r.success_runs)||0;
    const share = runs/totalRuns*100;
    const sr = runs ? (succ/runs*100) : 0;
    const name = ADAPTER_LABELS[r.adapter] || r.adapter || "Unknown";
    html += '<tr><td class="name">'+name+'</td><td>'+fmt(runs)+'</td><td>'+fmt(r.saved_tokens)
      +'</td><td>'+bytes(r.saved_bytes)+'</td><td>'+pct(sr)+'</td><td>'+share.toFixed(1)
      +'%<div class="bar"><i style="width:'+share.toFixed(1)+'%"></i></div></td></tr>';
  }
  html += '</tbody></table>';
  host.innerHTML = html;
}

function renderSurfaces(rows){
  const host = document.getElementById("surfaces");
  if(!rows.length){ host.innerHTML='<div class="empty">No data for this range.</div>'; return; }
  const totalRuns = rows.reduce((a,r)=>a+(Number(r.runs)||0),0) || 1;
  let html = '<table><thead><tr><th>Surface</th><th>Runs</th><th>Saved tokens</th><th>Saved bytes</th><th>Success</th><th>Share</th></tr></thead><tbody>';
  for(const r of rows){
    const runs = Number(r.runs)||0;
    const succ = Number(r.success_runs)||0;
    const share = runs/totalRuns*100;
    const sr = runs ? (succ/runs*100) : 0;
    const name = SURFACE_LABELS[r.surface] || r.surface || "Unknown";
    html += '<tr><td class="name">'+name+'</td><td>'+fmt(runs)+'</td><td>'+fmt(r.saved_tokens)
      +'</td><td>'+bytes(r.saved_bytes)+'</td><td>'+pct(sr)+'</td><td>'+share.toFixed(1)
      +'%<div class="bar"><i style="width:'+share.toFixed(1)+'%"></i></div></td></tr>';
  }
  html += '</tbody></table>';
  host.innerHTML = html;
}

function renderVersions(id,label,key,rows){
  const host = document.getElementById(id);
  if(!rows.length){ host.innerHTML='<div class="empty">No '+label+' data.</div>'; return; }
  const max = rows.reduce((a,r)=>Math.max(a,Number(r.runs)||0),0)||1;
  let html='<table><thead><tr><th>'+label+'</th><th>Runs</th></tr></thead><tbody>';
  for(const r of rows.slice(0,12)){
    const runs=Number(r.runs)||0;
    html+='<tr><td class="name">'+(r[key]||"—")+'</td><td>'+fmt(runs)
      +'<div class="bar"><i style="width:'+(runs/max*100).toFixed(1)+'%"></i></div></td></tr>';
  }
  html+='</tbody></table>';
  host.innerHTML=html;
}

function renderExternal(ext){
  const npm = ext.npm_downloads||{};
  const host = document.getElementById("npm");
  const order=[["last_day","Last day"],["last_week","Last week"],["last_month","Last month"]];
  const have = order.some(([k])=> typeof npm[k]==="number");
  if(have){
    let html='<table><thead><tr><th>npm downloads (xitsg)</th><th>Count</th></tr></thead><tbody>';
    for(const [k,lbl] of order){
      if(typeof npm[k]==="number") html+='<tr><td class="name">'+lbl+'</td><td>'+fmt(npm[k])+'</td></tr>';
    }
    html+='</tbody></table>';
    if(npm.as_of) html+='<div class="empty">as of '+npm.as_of+'</div>';
    host.innerHTML=html;
  }else{
    host.innerHTML='<div class="empty">npm downloads: no snapshot yet.</div>';
  }

  const vc = ext.vscode_installs||{};
  const vhost = document.getElementById("vscode-installs");
  if(vc && vc.value!=null){
    vhost.innerHTML='<div class="card" style="border:0;padding:0"><div class="k">VS Code Marketplace installs</div><div class="v">'+fmt(vc.value)+'</div></div>'
      +'<div class="empty">'+(vc.note||"")+(vc.as_of?(" · as of "+vc.as_of):"")+'</div>';
  }else{
    vhost.innerHTML='<div class="card" style="border:0;padding:0"><div class="k">VS Code Marketplace installs</div><div class="v">N/A</div></div>'
      +'<div class="empty">'+(vc.note||"not collected")+'</div>';
  }
}

// --- inline SVG charts (no deps) ---
function lineChart(id, pts){
  const svg = document.getElementById(id);
  const W=svg.clientWidth||520, H=svg.clientHeight||160, P=10;
  svg.setAttribute("viewBox","0 0 "+W+" "+H);
  if(pts.length<2){
    svg.innerHTML='<text x="'+(W/2)+'" y="'+(H/2)+'" fill="#6c6c75" font-size="12" text-anchor="middle">not enough data</text>';
    return;
  }
  const max=Math.max(1,...pts.map(p=>p.y));
  const sx=i=> P + i*(W-2*P)/(pts.length-1);
  const sy=v=> H-P - (v/max)*(H-2*P);
  let line="", area="M "+sx(0)+" "+(H-P);
  pts.forEach((p,i)=>{ const x=sx(i),y=sy(p.y); line+=(i?" L ":"M ")+x+" "+y; area+=" L "+x+" "+y; });
  area+=" L "+sx(pts.length-1)+" "+(H-P)+" Z";
  svg.innerHTML='<line class="axis" x1="'+P+'" y1="'+(H-P)+'" x2="'+(W-P)+'" y2="'+(H-P)+'"/>'
    +'<path class="area" d="'+area+'"/><path class="line" d="'+line+'"/>';
}

function multiAdapterChart(rows){
  const svg = document.getElementById("chart-adapters");
  const legend = document.getElementById("legend-adapters");
  legend.innerHTML="";
  const W=svg.clientWidth||1040, H=svg.clientHeight||200, P=10;
  svg.setAttribute("viewBox","0 0 "+W+" "+H);
  if(!rows.length){ svg.innerHTML='<text x="'+(W/2)+'" y="'+(H/2)+'" fill="#6c6c75" font-size="12" text-anchor="middle">not enough data</text>'; return; }
  const days=[...new Set(rows.map(r=>r.day))].sort();
  const adapters=[...new Set(rows.map(r=>r.adapter))];
  const idx=Object.fromEntries(days.map((d,i)=>[d,i]));
  const series={};
  adapters.forEach(a=> series[a]=days.map(()=>0));
  rows.forEach(r=>{ series[r.adapter][idx[r.day]] = Number(r.runs)||0; });
  let max=1; days.forEach((d,i)=> adapters.forEach(a=> max=Math.max(max, series[a][i])));
  const sx=i=> P + (days.length<2?0:i*(W-2*P)/(days.length-1));
  const sy=v=> H-P - (v/max)*(H-2*P);
  let svgInner='<line class="axis" x1="'+P+'" y1="'+(H-P)+'" x2="'+(W-P)+'" y2="'+(H-P)+'"/>';
  adapters.forEach((a,ai)=>{
    const c=ADAPTER_COLORS[ai%ADAPTER_COLORS.length];
    let line="";
    series[a].forEach((v,i)=>{ line+=(i?" L ":"M ")+sx(i)+" "+sy(v); });
    if(days.length<2){ line="M "+sx(0)+" "+sy(series[a][0]); }
    svgInner+='<path d="'+line+'" fill="none" stroke="'+c+'" stroke-width="1.6"/>';
    legend.appendChild(el("span",null,'<i style="background:'+c+'"></i>'+(ADAPTER_LABELS[a]||a)));
  });
  svg.innerHTML=svgInner;
}

function multiSurfaceChart(rows){
  const svg = document.getElementById("chart-surfaces");
  const legend = document.getElementById("legend-surfaces");
  legend.innerHTML="";
  const W=svg.clientWidth||1040, H=svg.clientHeight||200, P=10;
  svg.setAttribute("viewBox","0 0 "+W+" "+H);
  if(!rows.length){ svg.innerHTML='<text x="'+(W/2)+'" y="'+(H/2)+'" fill="#6c6c75" font-size="12" text-anchor="middle">not enough data</text>'; return; }
  const days=[...new Set(rows.map(r=>r.day))].sort();
  const surfaces=[...new Set(rows.map(r=>r.surface))];
  const idx=Object.fromEntries(days.map((d,i)=>[d,i]));
  const series={};
  surfaces.forEach(s=> series[s]=days.map(()=>0));
  rows.forEach(r=>{ series[r.surface][idx[r.day]] = Number(r.runs)||0; });
  let max=1; days.forEach((d,i)=> surfaces.forEach(s=> max=Math.max(max, series[s][i])));
  const sx=i=> P + (days.length<2?0:i*(W-2*P)/(days.length-1));
  const sy=v=> H-P - (v/max)*(H-2*P);
  let svgInner='<line class="axis" x1="'+P+'" y1="'+(H-P)+'" x2="'+(W-P)+'" y2="'+(H-P)+'"/>';
  surfaces.forEach((s,si)=>{
    const c=SURFACE_COLORS[si%SURFACE_COLORS.length];
    let line="";
    series[s].forEach((v,i)=>{ line+=(i?" L ":"M ")+sx(i)+" "+sy(v); });
    if(days.length<2){ line="M "+sx(0)+" "+sy(series[s][0]); }
    svgInner+='<path d="'+line+'" fill="none" stroke="'+c+'" stroke-width="1.6"/>';
    legend.appendChild(el("span",null,'<i style="background:'+c+'"></i>'+(SURFACE_LABELS[s]||s)));
  });
  svg.innerHTML=svgInner;
}

document.getElementById("ranges").addEventListener("click", e=>{
  const b=e.target.closest("button"); if(!b) return; load(b.dataset.range);
});
load("30d");
window.addEventListener("resize", ()=>{ /* re-render charts on resize */ });
</script>
</body>
</html>`;
