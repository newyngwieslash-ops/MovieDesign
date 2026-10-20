import { invoke } from '@tauri-apps/api/core';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useState } from 'react';
import { api, type Health, newRequestID } from '../../gateway';

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
  projectID,
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

  const policyQuery = useQuery({
    queryKey: ['update-policy', projectID || '_app'],
    queryFn: ({ signal }) =>
      api<PolicySnapshot>(
        projectID || null,
        '/api/updates/policy?version=0.1.0&platform=windows&locale=zh-CN',
        { signal }
      ),
    enabled: ready,
  });

  const checkMutation = useMutation({
    mutationFn: async () => {
      setErrorMessage('');
      const requestId = newRequestID();
      setLastCheckRequestID(requestId);
      const snap = await api<PolicySnapshot>(projectID || null, '/api/updates/check', {
        method: 'POST',
        body: JSON.stringify({
          requestId,
          version: '0.1.0',
          platform: 'windows',
          locale: 'zh-CN',
          refresh: true,
        }),
      });
      try {
        const report = await invoke<NativeUpdaterGateReport>('updater_check_and_gate', {
          currentVersion: snap.currentVersion,
        });
        setNativeReport(report);
      } catch {
        // Browser-only test context or updater endpoint not yet published; gateway policy snapshot remains authoritative.
      }
      return snap;
    },
    onSuccess: (snap) => {
      queryClient.setQueryData(['update-policy', projectID || '_app'], snap);
    },
    onError: (err: Error) => {
      setErrorMessage(err.message);
    },
  });

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

        <div className="update-actions">
          <button
            type="button"
            onClick={() => checkMutation.mutate()}
            disabled={!ready || checkMutation.isPending}
            data-testid="update-check-btn"
          >
            {checkMutation.isPending ? '正在检查策略与签名更新…' : '立即检查更新（记录 app_update_check）'}
          </button>
          <button
            type="button"
            disabled={!gate.canAutoInstall}
            data-testid="update-auto-install-btn"
          >
            {gate.canAutoInstall ? '允许自动下载并验签安装' : '自动下载/安装已由门禁阻止'}
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
