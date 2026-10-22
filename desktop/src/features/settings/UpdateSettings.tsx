import { Channel, invoke } from '@tauri-apps/api/core';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { type Health, newRequestID } from '../../gateway';

import { DecisionFrame } from '../questions/ConfirmationCard';

export type LocalizedText = {
  zh: string;
  en: string;
};

export type ManualOnlyRule = {
  platforms?: string[];
  min_version_inclusive?: string;
  max_version_exclusive?: string;
  reason?: LocalizedText;
};

export type CDNPolicy = {
  schema_version: number;
  enabled: boolean;
  min_supported_version: string;
  force_reason?: LocalizedText;
  manual_download_url?: string;
  updated_at?: string;
  manual_only?: ManualOnlyRule;
};

export type ResolvedUpdatePolicy = {
  forced: boolean;
  targetVersion: string | null;
  minSupportedVersion: string;
  requiredReason: string | null;
  manualDownloadUrl: string | null;
  manualOnly: boolean;
  manualRecoveryReason: string | null;
};

export type PolicySnapshot = {
  updateHistory?: {status: string;requestId: string;candidateId: string;receiptId?: string;version: string}[];
  status: 'ready' | 'failed';
  source: 'cdn' | 'cache' | 'none';
  stale: boolean;
  clockSkew?: boolean;
  cachedAt?: string;
  policyCacheAgeMs: number | null;
  policyFallbackReason: string | null;
  warnings?: string[];
  currentVersion: string;
  platform: string;
  region: string;
  channel: string;
  locale: string;
  policyUrl: string;
  manifestUrl: string;
  releasesUrl: string;
  policy: CDNPolicy | null;
  resolved: ResolvedUpdatePolicy;
  busy: boolean;
  busyReason?: string;
  autoUpdateAllowed: boolean;
};

export type NativeUpdaterGateReport = {
  candidateId?: string;
  policy?: PolicySnapshot;
  platform: string;
  currentVersion: string;
  manifestUrl: string;
  releasesUrl: string;
  policyStatus: string;
  manualOnly: boolean;
  busy: boolean;
  autoInstallAllowed: boolean;
  manifestVersion: string | null;
  signatureVerified: boolean;
  action: string;
  reason: string | null;
};

export function describeUpdateGateState(snap: PolicySnapshot | undefined, health?: Health): {
  canAutoInstall: boolean;
  badge: string;
  summary: string;
  manualUrl: string;
} {
  const defaultReleases = 'https://github.com/newyngwieslash-ops/MovieDesign/releases/latest';
  if (!snap) {
    return {
      canAutoInstall: false,
      badge: '读取策略中',
      summary: '正在读取本地 24 小时策略缓存或远端策略…',
      manualUrl: defaultReleases,
    };
  }
  const manualUrl = snap.resolved.manualDownloadUrl ?? snap.releasesUrl ?? defaultReleases;
  const gatewayBusy = snap.busy || (health !== undefined && (!health.idle || !health.safe_to_suspend));

  if (snap.status !== 'ready') {
    return {
      canAutoInstall: false,
      badge: '策略未就绪（仅手动入口）',
      summary: `更新策略获取失败（${snap.policyFallbackReason ?? 'unknown'}）且无可用 LKG 缓存。未伪造服务端 manual_only 规则，已禁用自动安装，仅保留 GitHub Releases 手动入口。`,
      manualUrl,
    };
  }
  if (snap.resolved.manualOnly) {
    return {
      canAutoInstall: false,
      badge: '命中 manual_only（禁止自动下载/安装）',
      summary: snap.resolved.requiredReason ?? snap.resolved.manualRecoveryReason ?? '当前版本命中 manual_only 规则，请从本仓库 GitHub Releases 手动下载签名安装包。',
      manualUrl,
    };
  }
  if (gatewayBusy) {
    return {
      canAutoInstall: false,
      badge: '活动任务保护中（禁止安装/重启）',
      summary: snap.busyReason ?? '存在正在执行的生成任务或未完成操作，生成期间不自动安装更新或重启。',
      manualUrl,
    };
  }
  if (snap.resolved.forced) {
    return {
      canAutoInstall: snap.autoUpdateAllowed,
      badge: '需要强制更新（允许签名自动升级）',
      summary: snap.resolved.requiredReason ?? `当前版本低于最低支持阈值 ${snap.resolved.minSupportedVersion}，可通过签名更新包升级。`,
      manualUrl,
    };
  }
  return {
    canAutoInstall: snap.autoUpdateAllowed,
    badge: snap.stale ? '策略就绪（LKG 过期缓存兜底）' : '策略就绪（允许签名自动更新）',
    summary: snap.stale
      ? `远端刷新失败（${snap.policyFallbackReason ?? 'offline'}），已保留并应用元数据匹配的 LKG 策略（已过期）。`
      : '当前版本满足最低支持版本阈值，策略门禁已就绪，可通过签名 manifest 检查并安装更新。',
    manualUrl,
  };
}

export function UpdateSettings({
  ready,
  projectID: _projectID,
  health,
}: {
  ready: boolean;
  projectID: string;
  health?: Health;
}) {
  const queryClient = useQueryClient();
  const [nativeReport, setNativeReport] = useState<NativeUpdaterGateReport | null>(null);
  const [lastCheckRequestID, setLastCheckRequestID] = useState<string>('');
  const [errorMessage, setErrorMessage] = useState<string>('');

  const key = 'moviedesign.update.pending.v1';
  type Intent = {candidateId: string; version: string; decision: 'approve'|'reject'; requestId?: string; installRequestId?: string};
  const [saved] = useState(() => {
    try {
      const raw = localStorage.getItem(key);
      if (raw === null) return {intent: null, error: ''};
      const value: unknown = JSON.parse(raw);
      const v = value as Intent;
      if (!v || !['candidateId','version'].every(k => typeof (v as Record<string,unknown>)[k] === 'string' && (v as Record<string,string>)[k].length > 0) || !['requestId','installRequestId'].every(k => (v as Record<string,unknown>)[k] === undefined || typeof (v as Record<string,unknown>)[k] === 'string') || !['approve','reject'].includes(v.decision)) throw Error('原更新请求损坏，请保留记录并核对状态。');
      return {intent: v, error: ''};
    } catch(e) { return {intent: null, error: (e as Error).message}; }
  });
  const [intent, setIntent] = useState<Intent|null>(saved.intent);
  const [storageError, setStorageError] = useState(saved.error);
  const [progress, setProgress] = useState('');
  const [deciding, setDeciding] = useState(false);
  const [canDiscard, setCanDiscard] = useState(false);
  const owner = useRef({alive:true, candidateId:'', locked:false});
  useEffect(() => { owner.current.alive=true; return () => {owner.current.alive=false;owner.current.candidateId='';}; }, []);
  const policyQuery = useQuery({
    queryKey: ['update-policy', '_app'],
    queryFn: () => invoke<PolicySnapshot>('updater_policy'),
    enabled: ready,
  });
  const checkMutation = useMutation({
    mutationFn: async () => {
      if (owner.current.locked || intent) return;
      owner.current.locked=true; owner.current.candidateId=''; setNativeReport(null); setErrorMessage('');
      try {
        const requestId = newRequestID(); setLastCheckRequestID(requestId);
        return await invoke<NativeUpdaterGateReport>('updater_check_and_gate', {requestId});
      } finally { owner.current.locked=false; }
    },
    onSuccess: (report) => {
      if (!report || !owner.current.alive) return;
      owner.current.candidateId=report.candidateId ?? '';setNativeReport(report);
      if(report.policy)queryClient.setQueryData(['update-policy','_app'],report.policy);
      void queryClient.invalidateQueries({queryKey:['update-policy','_app']});
    },
    onError: (err: Error) => { if(owner.current.alive)setErrorMessage(err.message ?? String(err)); },
  });
  async function decide(candidate: NativeUpdaterGateReport, decision: 'approve'|'reject') {
    if (!owner.current.alive || owner.current.locked || intent || storageError || owner.current.candidateId !== candidate.candidateId || !candidate.candidateId || !candidate.manifestVersion) return;
    owner.current.locked=true;setDeciding(true);setErrorMessage('');
    const original: Intent = {candidateId:candidate.candidateId,version:candidate.manifestVersion,decision,requestId:newRequestID(),installRequestId:newRequestID()};
    const encoded=JSON.stringify(original);
    const owns=()=>owner.current.alive && owner.current.candidateId===original.candidateId && localStorage.getItem(key)===encoded;
    try {
      if(localStorage.getItem(key)!==null)throw Error('已存在原更新请求，请先查询状态。');
      localStorage.setItem(key,encoded);setIntent(original);
    }catch(e){setStorageError((e as Error).message);owner.current.locked=false;setDeciding(false);return;}
    try {
      const receipt=await invoke<{status:string;requestId:string}>('updater_decide',{candidateId:original.candidateId,requestId:original.requestId,decision});
      if(!owns())return;
      if(decision==='reject') {localStorage.removeItem(key);setIntent(null);owner.current.candidateId='';setNativeReport(null);setProgress('已拒绝本次更新');return;}
      if(receipt.status!=='approve'||receipt.requestId!==original.requestId)throw Error('原生批准回执不匹配');
      const onProgress=new Channel<{stage:string;downloaded:number;total?:number}>();
      onProgress.onmessage=p=>{try{if(owns())setProgress(`${p.stage} · ${p.downloaded}${p.total ? ` / ${p.total}` : ''} bytes`);}catch(e){setStorageError((e as Error).message);}};
      await invoke('updater_install',{candidateId:original.candidateId,receiptId:receipt.requestId,requestId:original.installRequestId,onProgress});
      if(owns())setProgress('安装已开始；应用将退出。原请求回执已保留。');
    }catch(e){if(owner.current.alive)setErrorMessage(`${(e as Error).message ?? String(e)}。不会自动重试安装，请查询原更新请求。`);}
    finally {owner.current.locked=false;if(owner.current.alive)setDeciding(false);}
  }
  async function recover() {
    if(!intent||owner.current.locked||storageError)return;
    owner.current.locked=true;setDeciding(true);setCanDiscard(false);
    const encoded=JSON.stringify(intent);
    try {
      const receipt=await invoke<{status:string;requestId?:string;candidateId?:string;canDiscard?:boolean}>('updater_receipt',{receiptId:intent.installRequestId ?? '',decisionId:intent.requestId ?? '',candidateId:intent.candidateId,requestId:newRequestID()});
      if(owner.current.alive&&localStorage.getItem(key)===encoded){setProgress(`原安装请求：${receipt.status}${receipt.requestId ? ` · 原请求 ${receipt.requestId}` : ''}（仅查询，不重复安装）`);setCanDiscard(receipt.canDiscard===true&&receipt.candidateId===intent.candidateId&&['approve','reject','absent'].includes(receipt.status));}
    }catch(e){if(owner.current.alive)setErrorMessage((e as Error).message ?? String(e));}
    finally{owner.current.locked=false;if(owner.current.alive)setDeciding(false);}
  }
  async function discard() {
    if(!intent||owner.current.locked||!canDiscard||storageError)return;
    owner.current.locked=true;setDeciding(true);
    const encoded=JSON.stringify(intent);
    try {
      if(localStorage.getItem(key)!==encoded)throw Error('原更新请求已改变，请刷新核对。');
      await invoke('updater_discard',{candidateId:intent.candidateId,receiptId:intent.installRequestId ?? '',decisionId:intent.requestId ?? '',requestId:newRequestID()});
      if(!owner.current.alive||localStorage.getItem(key)!==encoded)return;
      localStorage.removeItem(key);setIntent(null);setNativeReport(null);owner.current.candidateId='';setCanDiscard(false);setProgress(`已结束尚未开始安装的更新（原确认 ${intent.requestId || '未保留'}，原安装 ${intent.installRequestId || '未保留'}，候选 ${intent.candidateId}）。可重新检查并确认新候选。`);
    }catch(e){if(owner.current.alive)setErrorMessage((e as Error).message ?? String(e));}
    finally{owner.current.locked=false;if(owner.current.alive)setDeciding(false);}
  }

  const snap = policyQuery.data;
  const gate = describeUpdateGateState(snap, health);

  return (
    <section className="update-settings" aria-label="版本与签名更新">
      <div className="canvas-header">
        <div>
          <h2>版本与签名更新</h2>
          <p>由 Rust tauri-plugin-updater 执行公钥验签，Gateway 提供 24 小时策略缓存、manual_only 门禁与活动状态保护。</p>
        </div>
        <span className="local-badge" data-testid="update-gate-badge">{gate.badge}</span>
      </div>

      {errorMessage && (
        <div className="error-banner" role="alert">
          <strong>更新检查失败</strong>
          <p>{errorMessage}</p>
        </div>
      )}
      {policyQuery.error && <p role="alert">无法读取原生更新状态：{policyQuery.error.message}</p>}

      <div className="update-card" data-testid="update-policy-card">
        <p data-testid="update-gate-summary">{gate.summary}</p>
        {snap && (
          <dl className="update-meta">
            <div>
              <dt>当前版本 / 平台映射</dt>
              <dd data-testid="update-version-platform">{snap.currentVersion} · {snap.platform}（{snap.region}/{snap.channel}）</dd>
            </div>
            <div>
              <dt>策略状态 / 来源</dt>
              <dd data-testid="update-status-source">
                {snap.status} / {snap.source}
                {snap.stale ? ' · 已过期（LKG 兜底）' : ''}
                {snap.clockSkew ? ' · 时钟回拨/未来时间戳已按过期处理' : ''}
              </dd>
            </div>
            <div>
              <dt>最低支持版本阈值</dt>
              <dd data-testid="update-min-supported">
                {snap.resolved.minSupportedVersion || '未加载'}（阈值不冒充实际待安装版本，实际版本取签名 manifest）
              </dd>
            </div>
            <div>
              <dt>缓存年龄 / 回退原因</dt>
              <dd data-testid="update-cache-age">
                {snap.policyCacheAgeMs !== null ? `${snap.policyCacheAgeMs} ms` : '无缓存'}
                {snap.policyFallbackReason ? ` · 回退原因：${snap.policyFallbackReason}` : ''}
              </dd>
            </div>
            <div>
              <dt>策略地址（GitHub main）</dt>
              <dd><code>{snap.policyUrl}</code></dd>
            </div>
            <div>
              <dt>签名 Manifest 地址（GitHub Releases）</dt>
              <dd><code>{snap.manifestUrl}</code></dd>
            </div>
          </dl>
        )}

        {snap?.warnings && snap.warnings.length > 0 && (
          <div role="status" className="update-warnings" data-testid="update-warnings">
            {snap.warnings.map((w, idx) => <p key={idx}>告警：{w}</p>)}
          </div>
        )}

        {nativeReport && (
          <div className="native-updater-report" data-testid="native-updater-report">
            <strong>原生签名更新器门禁回执：</strong>
            <span>动作 {nativeReport.action} · 平台 {nativeReport.platform} · 签名校验 {nativeReport.signatureVerified ? '通过' : '未执行/拦截'}</span>
            {nativeReport.reason && <p>{nativeReport.reason}</p>}
          </div>
        )}

        {nativeReport?.candidateId && nativeReport.autoInstallAllowed && <DecisionFrame title="应用更新确认">
          <p>从 {nativeReport.currentVersion} 更新至 {nativeReport.manifestVersion}。批准后下载并验证签名，安装时会关闭本应用及其本地服务。</p>
          <button data-testid="update-approve-btn" disabled={deciding || !!intent || !!storageError} onClick={()=>void decide(nativeReport,'approve')}>批准下载并安装此版本</button>
          <button data-testid="update-reject-btn" disabled={deciding || !!intent || !!storageError} onClick={()=>void decide(nativeReport,'reject')}>拒绝</button>
        </DecisionFrame>}
        {intent && <p>已保存原更新请求 {intent.installRequestId || intent.requestId || intent.candidateId}。<button disabled={deciding || !!storageError} onClick={()=>void recover()}>查询原更新请求</button>{canDiscard && <button disabled={deciding || !!storageError} onClick={()=>void discard()}>结束尚未开始安装的更新</button>}</p>}
        {progress && <p role="status">{progress}</p>}
        {storageError && <p role="alert">无法安全保存更新确认：{storageError}</p>}
        {!!snap?.updateHistory?.length && <details><summary>应用更新原请求账本</summary>{snap.updateHistory.map(r=><p key={`${r.status}-${r.requestId}`}>{r.version} · {r.status} · 原请求 {r.requestId}{r.status==='install_started'?'（安装意图已保存，结果待核对；不会自动重装）':''}</p>)}</details>}
        <div className="update-actions">
          <button
            type="button"
            onClick={() => checkMutation.mutate()}
            disabled={!ready || checkMutation.isPending || deciding || !!intent}
            data-testid="update-check-btn"
          >
            {checkMutation.isPending ? '正在检查策略与签名更新…' : '立即检查更新（记录 app_update_check）'}
          </button>
          <a
            href={gate.manualUrl}
            target="_blank"
            rel="noreferrer"
            data-testid="update-manual-link"
          >
            前往 GitHub Releases 手动下载签名安装包
          </a>
        </div>
        {lastCheckRequestID && (
          <small data-testid="update-last-request-id">最近检查幂等键：{lastCheckRequestID}</small>
        )}
      </div>
    </section>
  );
}
