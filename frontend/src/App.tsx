import { useState, useEffect, useRef, useCallback } from 'react';
import { EventsOn, EventsOff } from '../wailsjs/runtime/runtime';
import {
  GetConfig, SaveConfig, StartProxy, StopProxy, GetStatus,
  GetMetrics, GetLogs, ClearLogs, LoadCAPEMFromFile,
  TestUpstreamTLS, TestProxyAuth, TestCONNECT, TestHTTPGet,
  ListProfiles, SwitchProfile, CreateProfile, DuplicateProfile, DeleteProfile, RenameProfile,
} from '../wailsjs/go/main/App';
import type { ConfigDTO, MetricsSnapshot, LogEntry, CheckResult, ProfileSummary } from './types';

const PROFILE_NAME_REGEX = /^[A-Za-z0-9_-]{1,32}$/;
const PROFILE_NAME_ERROR = 'Profile name: 1-32 chars, letters/digits/hyphen/underscore only';

const emptyConfig: ConfigDTO = {
  active_profile: 'default',
  upstream_host: '', upstream_port: 3128, username: '', password: '',
  verify_tls: true, custom_ca_pem: '', connect_timeout: 30,
  idle_timeout: 300, bind_host: '127.0.0.1', bind_port: 18080,
};

type ProfileDialog =
  | { kind: 'none' }
  | { kind: 'create' }
  | { kind: 'duplicate' }
  | { kind: 'rename'; from: string }
  | { kind: 'delete'; name: string };

function App() {
  const [config, setConfig] = useState<ConfigDTO>(emptyConfig);
  const [profiles, setProfiles] = useState<ProfileSummary[]>([]);
  const [status, setStatus] = useState<'running' | 'stopped'>('stopped');
  const [metrics, setMetrics] = useState<MetricsSnapshot>({
    active_connections: 0, total_requests: 0, bytes_in: 0, bytes_out: 0, last_error: '',
  });
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [diagnosticResult, setDiagnosticResult] = useState<CheckResult | null>(null);
  const [diagnosticRunning, setDiagnosticRunning] = useState(false);
  const [saveMsg, setSaveMsg] = useState('');
  const [error, setError] = useState('');
  const [activeTab, setActiveTab] = useState<'config' | 'diagnostics' | 'activity' | 'help'>('config');
  const [profileDialog, setProfileDialog] = useState<ProfileDialog>({ kind: 'none' });
  const [profileNameInput, setProfileNameInput] = useState('');
  const logEndRef = useRef<HTMLDivElement>(null);
  const logIdRef = useRef(0);

  const refreshProfiles = useCallback(async () => {
    try {
      const list = await ListProfiles();
      setProfiles(list);
    } catch (e) {
      setError(String(e));
    }
  }, []);

  useEffect(() => {
    GetConfig().then(setConfig).catch(e => setError(String(e)));
    GetStatus().then(s => setStatus(s as 'running' | 'stopped'));
    GetLogs().then(setLogs);
    refreshProfiles();
  }, [refreshProfiles]);

  useEffect(() => {
    EventsOn('log', (entry: LogEntry) => {
      const id = ++logIdRef.current;
      setLogs(prev => [...prev.slice(-499), { ...entry, _id: id }]);
    });
    EventsOn('status', (s: string) => {
      setStatus(s as 'running' | 'stopped');
    });
    return () => {
      EventsOff('log');
      EventsOff('status');
    };
  }, []);

  useEffect(() => {
    const interval = setInterval(() => {
      if (status === 'running') {
        GetMetrics().then(setMetrics);
      }
    }, 1000);
    return () => clearInterval(interval);
  }, [status]);

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [logs]);

  const handleSave = useCallback(async () => {
    try {
      await SaveConfig(config);
      setSaveMsg('Saved');
      setError('');
      setTimeout(() => setSaveMsg(''), 2000);
    } catch (e) {
      setError(String(e));
    }
  }, [config]);

  const handleStart = async () => {
    try {
      await SaveConfig(config);
      await StartProxy();
      setError('');
    } catch (e) {
      setError(String(e));
    }
  };

  const handleStop = async () => {
    try {
      await StopProxy();
      setError('');
    } catch (e) {
      setError(String(e));
    }
  };

  // Profile: switch active. Refreshes profiles list before updating the form
  // so the dropdown's active flag never disagrees with the form contents.
  const handleSwitchProfile = async (name: string) => {
    if (name === config.active_profile) return;
    try {
      if (status === 'running') {
        const ok = window.confirm(
          `Proxy is running. Stop it and switch to "${name}"?`
        );
        if (!ok) return;
        await StopProxy();
      }
      const next = await SwitchProfile(name);
      await refreshProfiles();
      setConfig(next);
      setError('');
    } catch (e) {
      setError(String(e));
    }
  };

  // Profile: create dialog actions
  const openCreateProfile = () => {
    setProfileNameInput('');
    setProfileDialog({ kind: 'create' });
  };

  const openDuplicateProfile = () => {
    setProfileNameInput(`${config.active_profile}-copy`);
    setProfileDialog({ kind: 'duplicate' });
  };

  const openRenameProfile = () => {
    setProfileNameInput(config.active_profile);
    setProfileDialog({ kind: 'rename', from: config.active_profile });
  };

  const openDeleteProfile = () => {
    if (profiles.length <= 1) {
      setError('Cannot delete the last remaining profile');
      return;
    }
    setProfileDialog({ kind: 'delete', name: config.active_profile });
  };

  const closeProfileDialog = () => {
    setProfileDialog({ kind: 'none' });
    setProfileNameInput('');
  };

  const confirmProfileDialog = async () => {
    if (profileDialog.kind === 'none') return;
    // Create/duplicate/rename all need a valid name; delete does not.
    if (profileDialog.kind !== 'delete' && !PROFILE_NAME_REGEX.test(profileNameInput)) {
      setError(PROFILE_NAME_ERROR);
      return;
    }
    try {
      let next: ConfigDTO;
      if (profileDialog.kind === 'create') {
        next = await CreateProfile(profileNameInput);
      } else if (profileDialog.kind === 'duplicate') {
        next = await DuplicateProfile(config.active_profile, profileNameInput);
      } else if (profileDialog.kind === 'rename') {
        next = await RenameProfile(profileDialog.from, profileNameInput);
      } else {
        next = await DeleteProfile(profileDialog.name);
      }
      await refreshProfiles();
      setConfig(next);
      setError('');
      closeProfileDialog();
    } catch (e) {
      setError(String(e));
    }
  };

  const handleLoadCA = async () => {
    try {
      const pem = await LoadCAPEMFromFile();
      if (pem) setConfig(c => ({ ...c, custom_ca_pem: pem }));
    } catch (e) {
      setError(String(e));
    }
  };

  const runDiagnostic = async (fn: () => Promise<CheckResult>) => {
    setDiagnosticRunning(true);
    setDiagnosticResult(null);
    try {
      // Save current GUI values to Go backend before running the test,
      // so diagnostics always use the latest input.
      await SaveConfig(config);
      const result = await fn();
      setDiagnosticResult(result);
    } catch (e) {
      setDiagnosticResult({ ok: false, message: String(e), latency: '' });
    } finally {
      setDiagnosticRunning(false);
    }
  };

  const updateField = <K extends keyof ConfigDTO>(key: K, value: ConfigDTO[K]) => {
    setConfig(c => ({ ...c, [key]: value }));
  };

  const caBytes = config.custom_ca_pem?.length ?? 0;

  return (
    <div className="app">
      <header className="app-header">
        <h1>Burp Upstream HTTPS Proxy Adapter</h1>
        <div className="header-status">
          <div className="profile-selector">
            <span className="profile-selector-label">Profile</span>
            <select
              value={config.active_profile}
              onChange={e => handleSwitchProfile(e.target.value)}
            >
              {profiles.map(p => (
                <option key={p.name} value={p.name}>{p.name}</option>
              ))}
            </select>
          </div>
          <span className={`status-badge ${status}`}>{status.toUpperCase()}</span>
          {status === 'running' && (
            <span className="metric-badge">{metrics.active_connections} conn | {metrics.total_requests} req</span>
          )}
        </div>
      </header>

      {error && <div className="error-bar" onClick={() => setError('')}>{error}</div>}

      <nav className="tabs">
        {(['config', 'diagnostics', 'activity', 'help'] as const).map(tab => (
          <button key={tab} className={`tab ${activeTab === tab ? 'active' : ''}`}
            onClick={() => setActiveTab(tab)}>
            {tab === 'config' ? 'Configuration' : tab === 'diagnostics' ? 'Diagnostics' :
              tab === 'activity' ? 'Activity' : 'Burp Setup'}
          </button>
        ))}
      </nav>

      <main className="content">
        {activeTab === 'config' && (
          <div className="config-panel">
            <section className="section">
              <h2>Profile</h2>
              <div className="profile-actions">
                <span className="section-hint">Active: <strong>{config.active_profile}</strong> ({profiles.length} total)</span>
                <span className="spacer" />
                <button className="btn-small" onClick={openCreateProfile}>New</button>
                <button className="btn-small" onClick={openDuplicateProfile}>Duplicate</button>
                <button className="btn-small" onClick={openRenameProfile}>Rename</button>
                <button className="btn-small" onClick={openDeleteProfile} disabled={profiles.length <= 1}>Delete</button>
              </div>
            </section>

            <section className="section">
              <h2>Upstream Proxy</h2>
              <div className="form-grid">
                <label>Host
                  <input type="text" value={config.upstream_host}
                    onChange={e => updateField('upstream_host', e.target.value)}
                    placeholder="proxy.example.com" />
                </label>
                <label>Port
                  <input type="number" value={config.upstream_port}
                    onChange={e => updateField('upstream_port', parseInt(e.target.value) || 0)} />
                </label>
                <label>Username
                  <input type="text" value={config.username}
                    onChange={e => updateField('username', e.target.value)}
                    placeholder="proxy-user" />
                </label>
                <label>Password
                  <input type="password" value={config.password}
                    onChange={e => updateField('password', e.target.value)}
                    placeholder="stored in OS keychain" />
                </label>
                <label className="checkbox-label">
                  <input type="checkbox" checked={config.verify_tls}
                    onChange={e => updateField('verify_tls', e.target.checked)} />
                  Verify TLS Certificate
                  {!config.verify_tls && <span className="warn-inline"> (insecure)</span>}
                </label>
                <label>Custom CA PEM
                  <div className="file-picker">
                    <input type="text"
                      value={caBytes > 0 ? `Loaded (${caBytes} bytes, stored in profile)` : ''}
                      readOnly
                      placeholder="(optional)" />
                    <button onClick={handleLoadCA}>Load</button>
                    {caBytes > 0 && (
                      <button onClick={() => updateField('custom_ca_pem', '')}>Clear</button>
                    )}
                  </div>
                  <span className={`ca-pem-indicator ${caBytes > 0 ? 'loaded' : ''}`}>
                    {caBytes > 0
                      ? 'PEM content is embedded in the profile — no external file required'
                      : 'Load a .pem/.crt/.cer file; the content is copied into the profile'}
                  </span>
                </label>
                <label>Connect Timeout (sec)
                  <input type="number" value={config.connect_timeout}
                    onChange={e => updateField('connect_timeout', parseInt(e.target.value) || 1)} />
                </label>
                <label>Idle Timeout (sec)
                  <input type="number" value={config.idle_timeout}
                    onChange={e => updateField('idle_timeout', parseInt(e.target.value) || 1)} />
                </label>
              </div>
            </section>

            <section className="section">
              <h2>Local Listener</h2>
              <p className="section-hint">Shared across profiles</p>
              <div className="form-grid">
                <label>Bind Host
                  <input type="text" value={config.bind_host}
                    onChange={e => updateField('bind_host', e.target.value)} />
                </label>
                <label>Bind Port
                  <input type="number" value={config.bind_port}
                    onChange={e => updateField('bind_port', parseInt(e.target.value) || 0)} />
                </label>
              </div>
            </section>

            <div className="button-bar">
              <button className="btn-primary" onClick={handleSave}>
                Save {saveMsg && <span className="save-check">&#10003;</span>}
              </button>
              {status === 'stopped' ? (
                <button className="btn-start" onClick={handleStart}>Start Proxy</button>
              ) : (
                <button className="btn-stop" onClick={handleStop}>Stop Proxy</button>
              )}
            </div>
          </div>
        )}

        {activeTab === 'diagnostics' && (
          <div className="diagnostics-panel">
            <section className="section">
              <h2>Connection Tests</h2>
              <p className="section-hint">Save config before running tests. Tests use the active profile.</p>
              <div className="diag-buttons">
                <button disabled={diagnosticRunning}
                  onClick={() => runDiagnostic(TestUpstreamTLS)}>
                  Test Upstream TLS
                </button>
                <button disabled={diagnosticRunning}
                  onClick={() => runDiagnostic(TestProxyAuth)}>
                  Test Proxy Auth
                </button>
                <button disabled={diagnosticRunning}
                  onClick={() => runDiagnostic(() => TestCONNECT('example.com:443'))}>
                  Test CONNECT (example.com:443)
                </button>
                <button disabled={diagnosticRunning}
                  onClick={() => runDiagnostic(() => TestHTTPGet('http://example.com/'))}>
                  Test HTTP GET (example.com)
                </button>
              </div>
            </section>
            {diagnosticRunning && <div className="diag-spinner">Running...</div>}
            {diagnosticResult && (
              <section className={`diag-result ${diagnosticResult.ok ? 'ok' : 'fail'}`}>
                <div className="diag-status">{diagnosticResult.ok ? 'PASS' : 'FAIL'}</div>
                <div className="diag-message">{diagnosticResult.message}</div>
                {diagnosticResult.latency && (
                  <div className="diag-latency">Latency: {diagnosticResult.latency}</div>
                )}
              </section>
            )}
          </div>
        )}

        {activeTab === 'activity' && (
          <div className="activity-panel">
            <section className="section">
              <h2>Metrics</h2>
              <div className="metrics-grid">
                <div className="metric"><span className="metric-label">Active Connections</span><span className="metric-value">{metrics.active_connections}</span></div>
                <div className="metric"><span className="metric-label">Total Requests</span><span className="metric-value">{metrics.total_requests}</span></div>
                <div className="metric"><span className="metric-label">Bytes In</span><span className="metric-value">{formatBytes(metrics.bytes_in)}</span></div>
                <div className="metric"><span className="metric-label">Bytes Out</span><span className="metric-value">{formatBytes(metrics.bytes_out)}</span></div>
              </div>
              {metrics.last_error && (
                <div className="last-error">Last Error: {metrics.last_error}</div>
              )}
            </section>
            <section className="section">
              <div className="log-header">
                <h2>Live Logs</h2>
                <button className="btn-small" onClick={() => { ClearLogs(); setLogs([]); }}>Clear</button>
              </div>
              <div className="log-panel">
                {logs.length === 0 && <div className="log-empty">No log entries yet</div>}
                {logs.map((entry) => (
                  <div key={entry._id ?? entry.time} className={`log-entry level-${entry.level.toLowerCase()}`}>
                    <span className="log-time">{entry.time}</span>
                    <span className="log-level">{entry.level}</span>
                    <span className="log-msg">{entry.message}</span>
                  </div>
                ))}
                <div ref={logEndRef} />
              </div>
            </section>
          </div>
        )}

        {activeTab === 'help' && (
          <div className="help-panel">
            <section className="section">
              <h2>Burp Suite Upstream Proxy Settings</h2>
              <p>Configure Burp Suite's upstream proxy to route traffic through this adapter:</p>
              <div className="help-config">
                <table>
                  <tbody>
                    <tr><td>Destination host</td><td><code>*</code></td></tr>
                    <tr><td>Proxy host</td><td><code>{config.bind_host || '127.0.0.1'}</code></td></tr>
                    <tr><td>Proxy port</td><td><code>{config.bind_port || 18080}</code></td></tr>
                    <tr><td>Authentication type</td><td><code>None</code></td></tr>
                  </tbody>
                </table>
              </div>
              <h3>Setup Steps</h3>
              <ol>
                <li>Open Burp Suite &rarr; Settings &rarr; Network &rarr; Connections</li>
                <li>Under "Upstream proxy servers", click "Add"</li>
                <li>Set Destination host to <code>*</code></li>
                <li>Set Proxy host to <code>{config.bind_host || '127.0.0.1'}</code></li>
                <li>Set Proxy port to <code>{config.bind_port || 18080}</code></li>
                <li>Leave Authentication type as "None"</li>
                <li>Click "OK"</li>
              </ol>
              <h3>How It Works</h3>
              <div className="help-flow">
                <code>Burp Browser</code> &rarr; <code>Burp Proxy</code> &rarr;{' '}
                <code>This Adapter ({config.bind_host}:{config.bind_port})</code> &rarr;{' '}
                <code>HTTPS Proxy ({config.upstream_host}:{config.upstream_port})</code> &rarr;{' '}
                <code>Internet</code>
              </div>
              <p className="help-note">
                Authentication to the upstream HTTPS proxy is handled by this adapter.
                Burp does not need to know the upstream credentials.
              </p>
            </section>
          </div>
        )}
      </main>

      {profileDialog.kind !== 'none' && (
        <ProfileModal
          dialog={profileDialog}
          nameInput={profileNameInput}
          onNameChange={setProfileNameInput}
          onCancel={closeProfileDialog}
          onConfirm={confirmProfileDialog}
        />
      )}
    </div>
  );
}

interface ProfileModalProps {
  dialog: ProfileDialog;
  nameInput: string;
  onNameChange: (v: string) => void;
  onCancel: () => void;
  onConfirm: () => void;
}

function ProfileModal({ dialog, nameInput, onNameChange, onCancel, onConfirm }: ProfileModalProps) {
  if (dialog.kind === 'none') return null;

  // Per-kind copy keeps the modal's three render variants self-contained and
  // avoids ternary chains that fall through to a default label.
  const copy = (() => {
    switch (dialog.kind) {
      case 'create':    return { title: 'Create new profile',                  confirm: 'Create' };
      case 'duplicate': return { title: 'Duplicate profile',                   confirm: 'Duplicate' };
      case 'rename':    return { title: `Rename profile "${dialog.from}"`,     confirm: 'Rename' };
      case 'delete':    return { title: `Delete profile "${dialog.name}"?`,    confirm: 'Delete' };
    }
  })();
  const isDelete = dialog.kind === 'delete';

  return (
    <div className="modal-backdrop" onClick={onCancel}>
      <div className="modal" onClick={e => e.stopPropagation()}>
        <h3>{copy.title}</h3>
        {isDelete ? (
          <p>
            This removes the profile and its stored password from the OS keychain.
            This cannot be undone.
          </p>
        ) : (
          <>
            <p>Profile name (1-32 chars, letters, digits, hyphen, underscore):</p>
            <input
              type="text"
              value={nameInput}
              onChange={e => onNameChange(e.target.value)}
              autoFocus
              onKeyDown={e => {
                if (e.key === 'Enter') onConfirm();
                if (e.key === 'Escape') onCancel();
              }}
            />
          </>
        )}
        <div className="modal-actions">
          <button className="btn-secondary" onClick={onCancel}>Cancel</button>
          <button className={isDelete ? 'btn-danger' : 'btn-primary'} onClick={onConfirm}>
            {copy.confirm}
          </button>
        </div>
      </div>
    </div>
  );
}

function formatBytes(b: number): string {
  if (b === 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  const i = Math.min(Math.floor(Math.log(b) / Math.log(1024)), units.length - 1);
  return `${(b / Math.pow(1024, i)).toFixed(i > 0 ? 1 : 0)} ${units[i]}`;
}

export default App;
