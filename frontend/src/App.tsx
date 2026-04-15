import { useState, useEffect, useRef, useCallback } from 'react';
import { EventsOn } from '../wailsjs/runtime/runtime';
import {
  GetConfig, SaveConfig, StartProxy, StopProxy, GetStatus,
  GetMetrics, GetLogs, ClearLogs, SelectCAFile,
  TestUpstreamTLS, TestProxyAuth, TestCONNECT, TestHTTPGet,
} from '../wailsjs/go/main/App';
import type { ConfigDTO, MetricsSnapshot, LogEntry, CheckResult } from './types';

function App() {
  const [config, setConfig] = useState<ConfigDTO>({
    upstream_host: '', upstream_port: 3128, username: '', password: '',
    verify_tls: true, custom_ca_path: '', connect_timeout: 30,
    idle_timeout: 300, bind_host: '127.0.0.1', bind_port: 18080,
  });
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
  const logEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    GetConfig().then(setConfig).catch(e => setError(String(e)));
    GetStatus().then(s => setStatus(s as 'running' | 'stopped'));
    GetLogs().then(setLogs);
  }, []);

  useEffect(() => {
    EventsOn('log', (entry: LogEntry) => {
      setLogs(prev => [...prev.slice(-499), entry]);
    });
    EventsOn('status', (s: string) => {
      setStatus(s as 'running' | 'stopped');
    });
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

  const handleSelectCA = async () => {
    try {
      const path = await SelectCAFile();
      if (path) setConfig(c => ({ ...c, custom_ca_path: path }));
    } catch (e) {
      setError(String(e));
    }
  };

  const runDiagnostic = async (fn: () => Promise<CheckResult>) => {
    setDiagnosticRunning(true);
    setDiagnosticResult(null);
    try {
      await SaveConfig(config);
      const result = await fn();
      setDiagnosticResult(result);
    } catch (e) {
      setDiagnosticResult({ ok: false, message: String(e), latency: '' });
    }
    setDiagnosticRunning(false);
  };

  const updateField = <K extends keyof ConfigDTO>(key: K, value: ConfigDTO[K]) => {
    setConfig(c => ({ ...c, [key]: value }));
  };

  return (
    <div className="app">
      <header className="app-header">
        <h1>Burp Upstream HTTPS Proxy Adapter</h1>
        <div className="header-status">
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
                    <input type="text" value={config.custom_ca_path} readOnly
                      placeholder="(optional)" />
                    <button onClick={handleSelectCA}>Browse</button>
                    {config.custom_ca_path && (
                      <button onClick={() => updateField('custom_ca_path', '')}>Clear</button>
                    )}
                  </div>
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
              <p className="section-hint">Save config before running tests. Tests use current settings.</p>
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
            <section className="section metrics-section">
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
                {logs.map((entry, i) => (
                  <div key={i} className={`log-entry level-${entry.level.toLowerCase()}`}>
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
