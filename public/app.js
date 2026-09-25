/* GGstar frontend: config load + Alpine app. GGSTAR bootstrap is inline in index.html. */
async function loadGGStarConfig() {
  try {
    const res = await fetch('/api/config');
    if (!res.ok) return;
    const cfg = await res.json();
    Object.assign(GGSTAR, {
      chainIdHex: cfg.chainIdHex || GGSTAR.chainIdHex,
      chainId: cfg.chainId || GGSTAR.chainId,
      chainName: cfg.network || GGSTAR.chainName,
      rpcUrl: cfg.rpcUrl || GGSTAR.rpcUrl,
      explorerUrl: cfg.explorerUrl || GGSTAR.explorerUrl,
      nativeSymbol: cfg.nativeSymbol || GGSTAR.nativeSymbol,
      contractAddress: cfg.contractAddress || '',
      abi: Array.isArray(cfg.abi) ? cfg.abi : [],
      aiEnabled: !!cfg.aiEnabled,
      githubAuth: !!cfg.githubAuth,
      loaded: true
    });
  } catch (_) { /* template values remain as fallback */ }
}

function ggstar() {
  return {
    // config
    aiEnabled: GGSTAR.aiEnabled,
    githubAuth: GGSTAR.githubAuth,
    contractReady: /^0x[0-9a-fA-F]{40}$/.test(GGSTAR.contractAddress || ''),

    // GitHub identity
    signedIn: false,
    viewerLogin: '',
    viewerAvatar: '',
    csrfToken: '',
    signInUrl: '/auth/github',
    signOutUrl: '/auth/logout',

    // step 1
    username: '',
    loading: false,
    error: '',

    // wallet
    walletAddress: '',
    connecting: false,
    chainId: '',

    // step 2
    result: null,
    selectedAvatar: 1,
    selectedTitle: '',
    customTitle: '',
    selectedSkills: [],
    removedSkills: [],

    // step 3
    isMinting: false,
    rechecking: false,
    txHash: '',
    existingBadge: null,
    checkingBadge: false,
    claim: null,
    showModal: false,
    badge: null,
    snippet: { markdown: '', html: '', badgeUrl: '', verifyUrl: '', twitter: '' },
    copied: '',

    async init() {
      if (!GGSTAR.loaded) {
        await loadGGStarConfig();
        this.contractReady = /^0x[0-9a-fA-F]{40}$/.test(GGSTAR.contractAddress || '');
        this.aiEnabled = GGSTAR.aiEnabled;
        this.githubAuth = GGSTAR.githubAuth;
      }
      await this.loadMe();
      await this.loadSaved();

      if (window.ethereum) {
        window.ethereum.on('accountsChanged', (accs) => {
          this.walletAddress = accs && accs.length ? accs[0] : '';
          this.checkMinted();
        });
        window.ethereum.on('chainChanged', () => window.location.reload());
        // Passive reconnect (no popup): after a reload, show the wallet that is
        // already authorized and restore the minted marker + copy widget.
        try {
          const accs = await window.ethereum.request({ method: 'eth_accounts' });
          if (accs && accs.length) {
            this.walletAddress = accs[0];
            this.checkMinted();
          }
        } catch (_) { /* stay disconnected */ }
      }
    },

    // loadMe pulls the session identity and the CSRF token the API expects.
    async loadMe() {
      try {
        const res = await fetch('/api/me', { credentials: 'same-origin' });
        if (!res.ok) return;
        const me = await res.json();
        this.signedIn = !!me.authenticated;
        this.viewerLogin = me.login || '';
        // Self-only analyze: the analyzed profile is always the signed-in
        // account, so keep the legacy username state in sync.
        if (this.signedIn && this.viewerLogin) {
          this.username = this.viewerLogin;
        }
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

    // api wraps fetch so every state-changing call carries the CSRF token and a
    // 401 sends the user to GitHub instead of failing silently.
    async api(url, options = {}) {
      const opts = { credentials: 'same-origin', ...options };
      opts.headers = { ...(options.headers || {}) };
      if (opts.body && !opts.headers['Content-Type']) {
        opts.headers['Content-Type'] = 'application/json';
      }
      if (this.csrfToken) {
        opts.headers['X-Csrf-Token'] = this.csrfToken;
      }

      const res = await fetch(url, opts);
      if (res.status === 401) {
        window.location.href = this.signInUrl;
        throw new Error('Redirecting to GitHub sign-in…');
      }
      return res;
    },

    get shortAddr() {
      if (!this.walletAddress) return '';
      return this.walletAddress.slice(0, 6) + '…' + this.walletAddress.slice(-4);
    },
    get analyzedUsername() {
      return this.result?.analysis?.username || this.viewerLogin || this.username.trim();
    },
    // canMint mirrors the server-side gate in POST /api/token-uri.
    get canMint() {
      return this.signedIn
        && !!this.viewerLogin
        && this.viewerLogin.toLowerCase() === this.analyzedUsername.toLowerCase();
    },
    get avatarPadded() {
      return String(this.selectedAvatar).padStart(3, '0');
    },
    get finalTitle() {
      if (this.selectedTitle === '__custom__') return this.customTitle.trim() || 'Untitled';
      return this.selectedTitle || '';
    },
    get unlockedAchievements() {
      return (this.result?.achievements || []).filter(a => a.unlocked);
    },
    get txUrl() {
      return GGSTAR.explorerUrl + '/tx/' + this.txHash;
    },
    get tweetUrl() {
      if (this.snippet.twitter) return this.snippet.twitter;
      const text = 'I just minted my soulbound Skill Badge on BOT Chain for @' +
        (this.result?.analysis?.username || '') + ' (skill score ' +
        (this.result?.analysis?.skillScore || 0) + '/100). @BOTChain_ai #BOTChain #RWA #SBT';
      return 'https://twitter.com/intent/tweet?text=' + encodeURIComponent(text);
    },
    get badgeApiUrl() {
      return this.walletAddress ? '/api/badge/' + this.walletAddress : '#';
    },
    // alreadyMinted drives the "sudah mint" marker: banner + disabled mint
    // button, so nobody discovers the 1-badge-per-wallet rule via a revert.
    get alreadyMinted() {
      return !!(this.existingBadge && this.existingBadge.found);
    },
    get mintedTokenUrl() {
      if (!this.alreadyMinted) return '#';
      return GGSTAR.explorerUrl + '/token/' + GGSTAR.contractAddress +
        '?tokenId=' + (this.existingBadge.tokenId ?? 0);
    },
    // showRecheck is true when ownership is unproven for a reason a retry
    // could fix (pending tx, RPC hiccup). A username mismatch is final, so
    // no retry is offered for it.
    get showRecheck() {
      if (!this.txHash || this.claim?.verified) return false;
      const r = this.claim?.reason || '';
      return !r.includes('badge claims @');
    },

    async connectWallet() {
      if (!window.ethereum) {
        this.error = 'MetaMask not detected. Install the extension to mint on BOT Chain.';
        return;
      }
      this.connecting = true;
      this.error = '';
      try {
        const accounts = await window.ethereum.request({ method: 'eth_requestAccounts' });
        this.walletAddress = accounts[0] || '';
        await this.ensureNetwork();
        await this.checkMinted();
      } catch (e) {
        this.error = e?.message || 'Wallet connection rejected.';
      } finally {
        this.connecting = false;
      }
    },

    // checkMinted asks the server whether this wallet already owns a badge.
    // Cheap (one read-only call) and the single source for the minted marker.
    async checkMinted() {
      if (!this.walletAddress) {
        this.existingBadge = null;
        return;
      }
      this.checkingBadge = true;
      try {
        const res = await this.api('/api/badge/' + this.walletAddress);
        if (res.status === 404) {
          this.existingBadge = null;
          return;
        }
        const data = await res.json();
        this.existingBadge = res.ok && data.badge ? data.badge : null;
      } catch (_) {
        this.existingBadge = null;
      } finally {
        this.checkingBadge = false;
      }
    },

    async ensureNetwork() {
      try {
        const current = await window.ethereum.request({ method: 'eth_chainId' });
        this.chainId = current;
        if (current.toLowerCase() === GGSTAR.chainIdHex.toLowerCase()) return;

        try {
          await window.ethereum.request({
            method: 'wallet_switchEthereumChain',
            params: [{ chainId: GGSTAR.chainIdHex }]
          });
        } catch (switchErr) {
          if (switchErr?.code !== 4902 && switchErr?.code !== -32603) throw switchErr;
          await window.ethereum.request({
            method: 'wallet_addEthereumChain',
            params: [{
              chainId: GGSTAR.chainIdHex,
              chainName: GGSTAR.chainName,
              nativeCurrency: { name: 'BOT', symbol: GGSTAR.nativeSymbol || 'BOT', decimals: 18 },
              rpcUrls: [GGSTAR.rpcUrl],
              blockExplorerUrls: [GGSTAR.explorerUrl]
            }]
          });
        }
        this.chainId = await window.ethereum.request({ method: 'eth_chainId' });
      } catch (e) {
        this.error = 'Could not switch to ' + GGSTAR.chainName + ': ' + (e?.message || e);
      }
    },

    async analyze() {
      // Self-only: the server ignores any username and analyzes the
      // signed-in account, so always send the session login.
      const name = (this.viewerLogin || '').trim();
      if (!name || !this.signedIn || this.loading) return;
      this.username = name;

      this.loading = true;
      this.error = '';
      this.showModal = false;
      this.txHash = '';
      this.claim = null;

      try {
        const res = await this.api('/api/analyze', {
          method: 'POST',
          body: JSON.stringify({ username: name, achievements: true })
        });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || 'Analysis failed');

        this.hydrate(data);
      } catch (e) {
        this.error = e.message || 'Analysis failed';
      } finally {
        this.loading = false;
      }
    },

    // hydrate fills the result state from an analysis payload. It is shared
    // by analyze() (fresh) and loadSaved() (from the server-side store).
    hydrate(data) {
      this.result = data;
      this.selectedSkills = [...(data.analysis.topSkills || [])];
      this.removedSkills = [];
      this.selectedTitle = (data.analysis.suggestedTitles || [])[0] || '__custom__';
      this.customTitle = '';
      this.selectedAvatar = 1;
    },

    // loadSaved restores the last stored analysis so a page reload does not
    // force the user through the GitHub + AI pipeline again. A 404 simply
    // means "never analyzed" and is not an error.
    async loadSaved() {
      if (!this.signedIn) return;
      try {
        const res = await this.api('/api/my-analysis');
        if (res.status === 404) return;
        const data = await res.json();
        if (!res.ok) return;
        this.hydrate(data);
      } catch (_) { /* stay in the initial state */ }
    },

    removeSkill(index) {
      const [removed] = this.selectedSkills.splice(index, 1);
      if (removed) this.removedSkills.push(removed);
    },
    restoreSkill(index) {
      const [restored] = this.removedSkills.splice(index, 1);
      if (restored && !this.selectedSkills.includes(restored)) this.selectedSkills.push(restored);
    },

    async mintSBT() {
      if (this.isMinting) return;
      if (!this.walletAddress) { await this.connectWallet(); if (!this.walletAddress) return; }
      if (!this.contractReady) { this.error = 'Contract address is not configured on the server.'; return; }
      if (!this.selectedSkills.length) { this.error = 'Keep at least one skill on the badge.'; return; }
      if (!this.signedIn) { window.location.href = this.signInUrl; return; }
      if (!this.canMint) {
        this.error = 'You can only mint a badge for @' + this.viewerLogin + '.';
        return;
      }

      this.isMinting = true;
      this.error = '';

      try {
        await this.ensureNetwork();

        const uriRes = await this.api('/api/token-uri', {
          method: 'POST',
          body: JSON.stringify({
            username: this.analyzedUsername,
            title: this.finalTitle,
            summary: this.result?.analysis?.summary || '',
            dominantLanguage: this.result?.stats?.dominantLanguage || '',
            skillScore: this.result?.analysis?.skillScore || 0,
            totalStars: this.result?.stats?.totalStars || 0,
            publicRepos: this.result?.stats?.ownRepos || 0,
            followers: this.result?.stats?.followers || 0,
            avatarId: this.selectedAvatar,
            skills: this.selectedSkills
          })
        });
        const uriData = await uriRes.json();
        if (!uriRes.ok) throw new Error(uriData.error || 'Could not build metadata');

        const provider = new ethers.providers.Web3Provider(window.ethereum);
        const signer = provider.getSigner();
        const contract = new ethers.Contract(GGSTAR.contractAddress, GGSTAR.abi, signer);

        const tx = await contract.mintBadge(
          this.analyzedUsername,
          this.selectedAvatar,
          this.finalTitle,
          this.result?.analysis?.skillScore || 0,
          this.selectedSkills,
          uriData.tokenUri
        );
        this.txHash = tx.hash;
        await tx.wait();

        // Ask the server to confirm the badge against the signed-in identity.
        // A forged claim is recorded as unverified rather than hidden.
        await this.verifyClaim();
        await this.checkMinted();
        await this.loadSnippet();
        this.showModal = true;
      } catch (e) {
        this.error = this.friendlyError(e);
      } finally {
        this.isMinting = false;
      }
    },

    async verifyClaim() {
      if (!this.txHash) return;
      try {
        const res = await this.api('/api/claim', {
          method: 'POST',
          body: JSON.stringify({ txHash: this.txHash })
        });
        const data = await res.json();
        this.claim = res.ok ? data : { verified: false, reason: data.error || 'verification failed' };
      } catch (_) {
        this.claim = { verified: false, reason: 'verification request failed' };
      }
    },

    friendlyError(e) {
      const msg = e?.error?.message || e?.data?.message || e?.message || String(e);
      if (msg.includes('AlreadyMinted') || msg.includes('user rejected') === false && msg.includes('hasMinted')) {
        return 'This wallet already minted a badge (1 badge per wallet).';
      }
      if (msg.includes('user rejected') || e?.code === 4001) return 'Transaction rejected in MetaMask.';
      if (msg.includes('InvalidSkillScore')) return 'Skill score must be between 1 and 100.';
      if (msg.includes('InvalidAvatarId')) return 'Avatar id must be between 1 and 100.';
      if (msg.includes('Soulbound')) return 'This badge is soulbound and cannot be transferred.';
      return msg;
    },

    async loadSnippet(retries = 3) {
      for (let attempt = 0; attempt < retries; attempt++) {
        try {
          const res = await this.api('/api/snippet?address=' + this.walletAddress);
          const data = await res.json();
          if (res.ok && (data.markdown || data.html)) {
            this.snippet = data;
            this.badge = data.badge || null;
            return;
          }
        } catch (_) { /* retry below */ }
        if (attempt < retries - 1) {
          await new Promise(r => setTimeout(r, 2500));
        }
      }
      // Last resort: build a usable snippet locally so the copy boxes are
      // never empty, even when the chain cannot be read right now.
      this.snippet = this.fallbackSnippet();
      this.badge = null;
    },

    fallbackSnippet() {
      const badgeUrl = window.location.origin + '/api/badge/' + this.walletAddress + '.svg';
      const verify = this.txHash ? GGSTAR.explorerUrl + '/tx/' + this.txHash : GGSTAR.explorerUrl;
      const markdown = `[![GGstar Skill Badge](${badgeUrl})](${verify})`;
      const html = `<a href="${verify}" target="_blank" rel="noopener">\n  <img src="${badgeUrl}" alt="GGstar Skill Badge" width="480" />\n</a>`;
      return { markdown, html, badgeUrl, verifyUrl: verify, twitter: '' };
    },

    // openWidget reopens the badge modal on demand (e.g. after it was closed
    // or the page was reloaded), so the copy widget is never lost.
    async openWidget() {
      if (!this.walletAddress) {
        await this.connectWallet();
        if (!this.walletAddress) return;
      }
      await this.loadSnippet();
      this.showModal = true;
    },

    async recheckClaim() {
      if (this.rechecking || !this.txHash) return;
      this.rechecking = true;
      try {
        await this.verifyClaim();
        await this.loadSnippet(1);
      } finally {
        this.rechecking = false;
      }
    },

    async copy(text) {
      if (!text) return;
      try {
        await navigator.clipboard.writeText(text);
        this.copied = 'Copied to clipboard';
        setTimeout(() => { this.copied = ''; }, 2000);
      } catch (_) {
        this.copied = 'Copy failed. Select the text manually.';
      }
    }
  };
}
