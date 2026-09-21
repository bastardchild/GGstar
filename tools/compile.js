#!/usr/bin/env node
/**
 * Compiles contracts/ggstarSkillSBT.sol and writes the ABI into
 * config/contract.json while preserving the deployment fields.
 *
 * Usage (no local Node required):
 *   docker run --rm -v "$PWD:/w" -w /w node:22-alpine \
 *     sh -c "npm i solc@0.8.26 @openzeppelin/contracts@5.0.2 --no-audit --no-fund && node tools/compile.js"
 */
const fs = require('fs');
const path = require('path');
const solc = require('solc');

const CONTRACT = 'ggstarSkillSBT.sol';
const SOURCE = path.join('contracts', CONTRACT);
const CONFIG = path.join('config', 'contract.json');

if (!fs.existsSync(SOURCE)) {
  console.error(`missing ${SOURCE}`);
  process.exit(1);
}

const input = {
  language: 'Solidity',
  sources: { [CONTRACT]: { content: fs.readFileSync(SOURCE, 'utf8') } },
  settings: {
    optimizer: { enabled: true, runs: 200 },
    outputSelection: {
      '*': { '*': ['abi', 'evm.bytecode.object', 'evm.deployedBytecode.object'] },
    },
  },
};

function findImport(importPath) {
  try {
    return { contents: fs.readFileSync(path.join('node_modules', importPath), 'utf8') };
  } catch (e) {
    return { error: `File not found: ${importPath}` };
  }
}

const output = JSON.parse(solc.compile(JSON.stringify(input), { import: findImport }));

let failed = false;
for (const err of output.errors || []) {
  console.log(`${err.severity.toUpperCase()}: ${err.formattedMessage.split('\n')[0]}`);
  if (err.severity === 'error') failed = true;
}
if (failed) process.exit(1);

const compiled = output.contracts[CONTRACT].ggstarSkillSBT;

let config = {};
if (fs.existsSync(CONFIG)) {
  config = JSON.parse(fs.readFileSync(CONFIG, 'utf8'));
}

config.network = config.network || 'BOT Chain Testnet';
config.chainId = config.chainId || 968;
config.chainIdHex = config.chainIdHex || '0x3c8';
config.rpcUrl = config.rpcUrl || 'https://rpc.bohr.life';
config.explorerUrl = config.explorerUrl || 'https://scan.bohr.life';
config.faucetUrl = config.faucetUrl || 'https://faucet.botchain.ai';
config.solidity = solc.version();
config.openzeppelin = '5.0.2';
config.deployed = config.deployed || false;
config.contractAddress = config.contractAddress || '';
config.deployTxHash = config.deployTxHash || '';
config.abi = compiled.abi;

fs.writeFileSync(CONFIG, JSON.stringify(config, null, 2));

console.log(`COMPILE_OK solc=${solc.version()} abi_entries=${compiled.abi.length}`);
console.log(`bytecode_bytes=${compiled.evm.bytecode.object.length / 2}`);
console.log(`wrote ${CONFIG}`);
console.log('Next: deploy via Remix, then set contractAddress/deployTxHash/deployed=true.');
