import { useState, useEffect, useCallback, useRef } from 'react';
import './App.css';
import { GetStatus, ToggleProxy, ImportURL, SaveConfig, GetConfig, GetImportedURLs } from '../wailsjs/go/main/App';

function App() {
  const [isConnected, setIsConnected] = useState(false);
  const [importURL, setImportURL] = useState('');
  const [localPort, setLocalPort] = useState('1080');
  const [isDarkMode, setIsDarkMode] = useState(true);
  const [uptime, setUptime] = useState(0);

  const [statusMsg, setStatusMsg] = useState('已就绪');
  const [errorMsg, setErrorMsg] = useState('');
  const [upstream, setUpstream] = useState('');
  const [showSettings, setShowSettings] = useState(false);
  const [settingsAddress, setSettingsAddress] = useState(':1080');
  const [settingsUpstream, setSettingsUpstream] = useState('');
  const [settingsUsername, setSettingsUsername] = useState('');
  const [settingsPassword, setSettingsPassword] = useState('');
  const [importedURLs, setImportedURLs] = useState<Array<{host: string, created_at: string}>>([]);

  const intervalRef = useRef<number | null>(null);

  const fetchStatus = useCallback(async () => {
    try {
      const status: any = await GetStatus();
      setIsConnected(status.running);
      setStatusMsg(status.statusMsg || (status.running ? '已连接' : '已就绪'));
      setErrorMsg(status.errorMsg || '');
      setUpstream(status.upstream || '');
      setLocalPort((status.localAddress || ':1080').replace(':', ''));
    } catch (err) {
      console.error('Failed to get status:', err);
    }
  }, []);

  const fetchConfig = useCallback(async () => {
    try {
      const cfg: any = await GetConfig();
      setSettingsAddress(cfg.localAddress || ':1080');
      setSettingsUpstream(cfg.upstream || '');
      setSettingsUsername(cfg.username || '');
      setSettingsPassword(cfg.password || '');
    } catch (err) {
      console.error('Failed to get config:', err);
    }
  }, []);

  const fetchImportedURLs = useCallback(async () => {
    try {
      const urls: any = await GetImportedURLs();
      setImportedURLs(urls || []);
    } catch (err) {
      console.error('Failed to get URLs:', err);
    }
  }, []);

  useEffect(() => {
    fetchStatus();
    fetchConfig();
    fetchImportedURLs();
    const timer = setInterval(fetchStatus, 2000);
    return () => clearInterval(timer);
  }, [fetchStatus, fetchConfig, fetchImportedURLs]);

  useEffect(() => {
    if (isConnected) {
      intervalRef.current = window.setInterval(() => {
        setUptime((prev) => prev + 1);
      }, 1000);
    } else {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
        intervalRef.current = null;
      }
      setUptime(0);

    }
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [isConnected]);

  // Fetch config when entering settings page
  useEffect(() => {
    if (showSettings) {
      fetchConfig();
    }
  }, [showSettings, fetchConfig]);

  const formatTime = (seconds: number): string => {
    const h = Math.floor(seconds / 3600).toString().padStart(2, '0');
    const m = Math.floor((seconds % 3600) / 60).toString().padStart(2, '0');
    const s = (seconds % 60).toString().padStart(2, '0');
    return h + ':' + m + ':' + s;
  };

  const handleToggle = async () => {
    const result: any = await ToggleProxy();
    setStatusMsg(result.message);
    await fetchStatus();
  };

  const handleImportURL = async () => {
    if (!importURL.trim()) return;
    const result: any = await ImportURL(importURL.trim());
    setStatusMsg(result.message);
    if (result.success) {
      setImportURL('');
      await fetchImportedURLs();
      await fetchStatus();
    }
  };

  const handleSaveSettings = async () => {
    const config = {
      localAddress: settingsAddress || ':1080',
      upstream: settingsUpstream,
      username: settingsUsername,
      password: settingsPassword,
    };
    const result: any = await SaveConfig(config);
    setStatusMsg(result.message);
    if (result.success) {
      await fetchConfig();
      await fetchStatus();
    }
  };

  const svgPower = 'M18.36 6.64a9 9 0 1 1-12.73 0';
  const svgActivity = 'M22 12h-4l-3 9L9 3l-3 9H2';

  return (
    <div className={'min-h-screen w-full flex flex-col items-center justify-center transition-colors duration-500 font-sans ' + (isDarkMode ? 'dark bg-[#121212]' : 'bg-[#e5e7eb]')}>
      <div className="absolute inset-0 overflow-hidden pointer-events-none flex justify-center items-center">
        <div className={'w-[600px] h-[600px] rounded-full mix-blend-multiply filter blur-[120px] opacity-30 transition-all duration-1000 ' + (isDarkMode ? 'bg-indigo-600' : 'bg-blue-400') + ' translate-x-1/3 -translate-y-1/4'}></div>
        <div className={'w-[500px] h-[500px] rounded-full mix-blend-multiply filter blur-[120px] opacity-30 transition-all duration-1000 ' + (isDarkMode ? 'bg-purple-600' : 'bg-pink-400') + ' -translate-x-1/3 translate-y-1/4'}></div>
      </div>
      <div className="absolute top-6 right-6 z-50">
        <button onClick={() => setIsDarkMode(!isDarkMode)} className="p-3 rounded-full bg-white/20 dark:bg-black/20 backdrop-blur-md shadow-lg border border-black/5 dark:border-white/10 text-slate-700 dark:text-slate-300 hover:scale-110 transition-all active:scale-95">
          {isDarkMode ? (
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
          ) : (
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
          )}
        </button>
      </div>
      <div className="relative w-[360px] h-[620px] rounded-[18px] shadow-2xl overflow-hidden flex flex-col border border-white/50 dark:border-white/10 bg-white/50 dark:bg-[#1e1e1e]/60 backdrop-blur-3xl transition-colors duration-500">
        <div className="flex-1 flex flex-col pt-6 pb-4 px-6 relative z-10">
          <div className="flex flex-col items-center mt-6">
            <button onClick={handleToggle} className="relative focus:outline-none select-none">
              <div className={'absolute inset-0 rounded-full transition-all duration-700 ease-out scale-110 ' + (isConnected ? 'bg-green-500/20 dark:bg-green-400/20 animate-ping' : 'bg-slate-300/30 dark:bg-black/30')}></div>
              <div className={'relative w-28 h-28 rounded-full flex items-center justify-center transition-all duration-500 ' + (isConnected ? 'bg-gradient-to-b from-green-400 to-green-600 shadow-[0_10px_30px_rgba(34,197,94,0.4)] scale-100 border border-green-300/50' : 'bg-gradient-to-b from-white to-slate-100 dark:from-[#3a3a3a] dark:to-[#2d2d2d] shadow-md border border-slate-200/50 dark:border-white/5 scale-100')}>
                <svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" className={'transition-colors duration-500 ' + (isConnected ? 'text-white drop-shadow-md' : 'text-slate-400 dark:text-slate-500')}>
                  <path d={svgPower}/>
                  <line x1="12" y1="2" x2="12" y2="12"/>
                </svg>
              </div>
            </button>
            <div className="text-center mt-8 space-y-1.5 h-16">
              <h2 className="text-[22px] font-semibold tracking-tight text-slate-800 dark:text-slate-100 transition-colors">
                {isConnected ? '已连接' + (upstream ? ' (HK)' : '') : '已就绪'}
              </h2>
              <div className="h-6 overflow-hidden">
                <div className={'transition-transform duration-500 flex flex-col items-center ' + (isConnected ? '-translate-y-6' : 'translate-y-0')}>
                  <p className="h-6 text-sm text-slate-500 dark:text-slate-400">{isConnected ? statusMsg : '点击按钮开启安全连接'}</p>
                  <p className="h-6 text-sm font-mono text-green-600 dark:text-green-400 flex items-center space-x-1.5">
                    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="animate-pulse"><polyline points={svgActivity}/></svg>
                    <span>{formatTime(uptime)}</span>
                  </p>
                </div>
              </div>
            </div>
            {errorMsg && (
              <div className="mt-2 px-3 py-1.5 bg-red-500/10 border border-red-500/20 rounded-lg">
                <p className="text-xs text-red-500 dark:text-red-400">{errorMsg}</p>
              </div>
            )}
          </div>

          <div className={'w-full h-px bg-gradient-to-r from-transparent via-slate-200 dark:via-white/10 to-transparent transition-all duration-500 ' + (isConnected ? 'mb-6' : 'my-6')}></div>
          {showSettings ? (
            <div className="flex flex-col space-y-4">
              <div className="flex flex-col space-y-1">
                <span className="text-[11px] font-semibold text-slate-500 dark:text-slate-400 ml-2 uppercase tracking-wider">监听地址</span>
                <div className="flex items-center bg-white/60 dark:bg-black/20 border border-slate-200/50 dark:border-white/5 rounded-xl p-1.5 backdrop-blur-md shadow-sm">
                  <input type="text" value={settingsAddress} onChange={(e) => setSettingsAddress(e.target.value)} className="flex-1 bg-transparent border-none outline-none text-[13px] text-slate-700 dark:text-slate-200 placeholder-slate-400 px-2" placeholder=":1080"/>
                </div>
              </div>
              <div className="flex flex-col space-y-1">
                <span className="text-[11px] font-semibold text-slate-500 dark:text-slate-400 ml-2 uppercase tracking-wider">上游代理</span>
                <div className="flex items-center bg-white/60 dark:bg-black/20 border border-slate-200/50 dark:border-white/5 rounded-xl p-1.5 backdrop-blur-md shadow-sm">
                  <input type="text" value={settingsUpstream} onChange={(e) => setSettingsUpstream(e.target.value)} className="flex-1 bg-transparent border-none outline-none text-[13px] text-slate-700 dark:text-slate-200 placeholder-slate-400 px-2" placeholder="socks5://host:port 或 mino://..."/>
                </div>
              </div>
              <div className="flex space-x-2">
                <div className="flex-1 flex flex-col space-y-1">
                  <span className="text-[11px] font-semibold text-slate-500 dark:text-slate-400 ml-2 uppercase tracking-wider">用户名（可选）</span>
                  <input type="text" value={settingsUsername} onChange={(e) => setSettingsUsername(e.target.value)} className="bg-white/60 dark:bg-black/20 border border-slate-200/50 dark:border-white/5 rounded-xl p-2 text-[13px] text-slate-700 dark:text-slate-200 placeholder-slate-400 outline-none" placeholder="可选"/>
                </div>
                <div className="flex-1 flex flex-col space-y-1">
                  <span className="text-[11px] font-semibold text-slate-500 dark:text-slate-400 ml-2 uppercase tracking-wider">密码（可选）</span>
                  <input type="password" value={settingsPassword} onChange={(e) => setSettingsPassword(e.target.value)} className="bg-white/60 dark:bg-black/20 border border-slate-200/50 dark:border-white/5 rounded-xl p-2 text-[13px] text-slate-700 dark:text-slate-200 placeholder-slate-400 outline-none" placeholder="可选"/>
                </div>
              </div>
              <button onClick={handleSaveSettings} className="w-full bg-slate-800 hover:bg-slate-700 dark:bg-slate-200 dark:hover:bg-white text-white dark:text-slate-800 text-sm font-medium py-2.5 rounded-xl transition-all shadow-sm mt-2">保存配置</button>
            </div>
          ) : (
            <>
              <div className="flex flex-col space-y-2 mb-4">
                <span className="text-[11px] font-semibold text-slate-500 dark:text-slate-400 ml-2 uppercase tracking-wider">节点订阅</span>
                <div className="flex items-center bg-white/60 dark:bg-black/20 border border-slate-200/50 dark:border-white/5 rounded-xl p-1.5 backdrop-blur-md shadow-sm focus-within:ring-2 focus-within:ring-blue-500/30">
                  <input type="text" placeholder="https://example.com/sub..." value={importURL} onChange={(e) => setImportURL(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && handleImportURL()} className="flex-1 w-0 bg-transparent border-none outline-none text-[13px] text-slate-700 dark:text-slate-200 placeholder-slate-400 dark:placeholder-slate-500 px-2"/>
                  <button onClick={handleImportURL} className="bg-slate-800 hover:bg-slate-700 dark:bg-slate-200 dark:hover:bg-white text-white dark:text-slate-800 text-xs font-medium py-1.5 px-3 rounded-lg transition-colors shadow-sm whitespace-nowrap">导入</button>
                </div>
              </div>
              <div className="flex flex-col space-y-2">
                <span className="text-[11px] font-semibold text-slate-500 dark:text-slate-400 ml-2 uppercase tracking-wider">
                  <span>高级设置</span>
                  <button onClick={() => setShowSettings(true)} className="text-[11px] text-blue-500 hover:text-blue-400 transition-colors ml-2">更多设置 →</button>
                </span>
                <div className="flex items-center justify-between bg-white/60 dark:bg-black/20 border border-slate-200/50 dark:border-white/5 rounded-xl p-3 backdrop-blur-md shadow-sm">
                  <div className="flex items-center space-x-3">
                    <div className="p-1.5 bg-slate-100 dark:bg-[#333] rounded-md">
                      <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="text-slate-500 dark:text-slate-400"><polyline points="16 16 12 12 8 16"/><line x1="12" y1="12" x2="12" y2="21"/><path d="M20.39 18.39A5 5 0 0 0 18 9h-1.26A8 8 0 1 0 3 16.3"/></svg>
                    </div>
                    <span className="text-[13px] font-medium text-slate-700 dark:text-slate-200">本地 Socks5/HTTP 端口</span>
                  </div>
                  <input type="text" value={localPort} onChange={(e) => setLocalPort(e.target.value)} className="w-[60px] bg-slate-100/50 dark:bg-[#2d2d2d]/80 border border-slate-200/80 dark:border-white/10 rounded-lg py-1 px-2 text-[13px] text-center font-mono text-slate-700 dark:text-slate-200 outline-none focus:ring-2 focus:ring-blue-500/50 shadow-inner"/>
                </div>
              </div>
              {importedURLs.length > 0 && (
                <div className="mt-3">
                  <div className="max-h-20 overflow-y-auto space-y-1">
                    {importedURLs.slice(-3).reverse().map((item, i) => (
                      <div key={i} className="flex items-center text-[10px] text-slate-400 dark:text-slate-500 px-2 py-1 bg-white/20 dark:bg-black/10 rounded-lg truncate">
                        <svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" className="mr-1.5 shrink-0"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>
                        <span className="truncate">{item.host}</span>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </>
          )}
          <div className="flex-1"></div>
          <div className="text-center pb-2">
            <button onClick={() => setShowSettings(!showSettings)} className="text-[11px] text-slate-400 dark:text-slate-500 hover:text-slate-600 dark:hover:text-slate-300 transition-colors font-medium">
              mino v1.0.0 • {showSettings ? '返回主界面' : '设置'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

export default App;
