/* GGstar standalone navbar state for pages without the full ggstar() app
   (leaderboard, achievements). The index page does NOT use this: its header
   inherits the outer ggstar() scope so the wallet address stays shared with
   the mint flow. Kept deliberately small; wallet logic mirrors app.js. */
function ggstarNav() {
  return {
    signedIn: false,
    viewerLogin: '',
    viewerAvatar: '',
    csrfToken: '',
    signInUrl: '/auth/github',
    signOutUrl: '/auth/logout',

    walletAddress: '',
    connecting: false,
    chainId: '',

    navChainIdHex: '',
    navChainName: '',
    navRpcUrl: '',
    navExplorerUrl: '',
    navNativeSymbol: 'BOT',

    async init() {
      await this.loadConfig();
      await this.loadMe();

      if (window.ethereum) {
        window.ethereum.on('accountsChanged', (accs) => {
          this.walletAddress = accs && accs.length ? accs[0] : '';
        });
        window.ethereum.on('chainChanged', () => window.location.reload());
        // Passive reconnect: show the already-connected wallet without a popup.
        // No network switch here on purpose: switching may prompt MetaMask,
        // and a bare page visit must never pop the wallet. The switch happens
        // on Connect click instead.
        try {
          const accs = await window.ethereum.request({ method: 'eth_accounts' });
          if (accs && accs.length) {
            this.walletAddress = accs[0];
          }
        } catch (_) { /* stay disconnected */ }
      }
    },

    async loadConfig() {
      try {
        const res = await fetch('/api/config');
        if (!res.ok) return;
        const cfg = await res.json();
        this.navChainIdHex = cfg.chainIdHex || '';
        this.navChainName = cfg.network || '';
        this.navRpcUrl = cfg.rpcUrl || '';
        this.navExplorerUrl = cfg.explorerUrl || '';
        this.navNativeSymbol = cfg.nativeSymbol || 'BOT';
      } catch (_) { /* defaults stand */ }
    },

    async loadMe() {
      try {
        const res = await fetch('/api/me', { credentials: 'same-origin' });
        if (!res.ok) return;
        const me = await res.json();
        this.signedIn = !!me.authenticated;
        this.viewerLogin = me.login || '';
        this.viewerAvatar = me.avatarUrl || '';
        this.csrfToken = me.csrfToken || '';
        this.signInUrl = me.signInUrl || '/auth/github';
        this.signOutUrl = me.signOutUrl || '/auth/logout';
      } catch (_) { /* stay anonymous */ }
    },

    async signOut() {
      try {
        await fetch(this.signOutUrl, {
          method: 'POST',
          credentials: 'same-origin',
          headers: { 'X-Csrf-Token': this.csrfToken },
        });
      } catch (_) { /* ignore */ }
      window.location.href = '/';
    },

    get shortAddr() {
      if (!this.walletAddress) return '';
      return this.walletAddress.slice(0, 6) + '…' + this.walletAddress.slice(-4);
    },

    async connectWallet() {
      if (!window.ethereum) return;
      this.connecting = true;
      try {
        const accounts = await window.ethereum.request({ method: 'eth_requestAccounts' });
        this.walletAddress = accounts[0] || '';
        await this.ensureNetwork();
      } catch (_) { /* rejected */ } finally {
        this.connecting = false;
      }
    },

    // No error surface exists in the slim header, so a failed auto-switch
    // must never toast or throw.
    async ensureNetwork() {
      if (!window.ethereum || !this.navChainIdHex) return;
      try {
        const current = await window.ethereum.request({ method: 'eth_chainId' });
        this.chainId = current;
        if (current.toLowerCase() === this.navChainIdHex.toLowerCase()) return;

        try {
          await window.ethereum.request({
            method: 'wallet_switchEthereumChain',
            params: [{ chainId: this.navChainIdHex }]
          });
        } catch (switchErr) {
          if (switchErr?.code !== 4902 && switchErr?.code !== -32603) throw switchErr;
          await window.ethereum.request({
            method: 'wallet_addEthereumChain',
            params: [{
              chainId: this.navChainIdHex,
              chainName: this.navChainName,
              nativeCurrency: { name: 'BOT', symbol: this.navNativeSymbol || 'BOT', decimals: 18 },
              rpcUrls: [this.navRpcUrl],
              blockExplorerUrls: [this.navExplorerUrl]
            }]
          });
        }
        this.chainId = await window.ethereum.request({ method: 'eth_chainId' });
      } catch (_) { /* quiet: no error slot in slim header */ }
    }
  };
}
