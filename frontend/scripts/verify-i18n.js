#!/usr/bin/env node

/**
 * 多语言文件键结构验证脚本
 * 用于检查所有语言文件的键结构是否一致
 */

import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const localesDir = path.join(__dirname, '../src/locales');
const localeFiles = [
  'zh-CN.json',
  'zh-TW.json',
  'en-US.json',
  'ja-JP.json',
  'ko-KR.json',
  'de-DE.json',
  'fr-FR.json',
  'es-ES.json',
  'pt-BR.json'
];

// 读取所有语言文件
const locales = {};
for (const file of localeFiles) {
  const filePath = path.join(localesDir, file);
  try {
    const content = fs.readFileSync(filePath, 'utf-8');
    locales[file] = JSON.parse(content);
  } catch (error) {
    console.error(`❌ 无法读取文件: ${file}`);
    console.error(error.message);
    process.exit(1);
  }
}

// 获取所有键的路径
function getKeys(obj, prefix = '') {
  let keys = [];
  for (const key in obj) {
    const fullKey = prefix ? `${prefix}.${key}` : key;
    if (typeof obj[key] === 'object' && obj[key] !== null) {
      keys = keys.concat(getKeys(obj[key], fullKey));
    } else {
      keys.push(fullKey);
    }
  }
  return keys;
}

// 获取基准语言的键（使用简体中文作为基准）
const baseFile = 'zh-CN.json';
const baseKeys = getKeys(locales[baseFile]).sort();

console.log('🔍 开始验证多语言文件键结构...\n');
console.log(`📋 基准语言: ${baseFile}`);
console.log(`📊 基准键数量: ${baseKeys.length}\n`);

let hasErrors = false;

// 检查每个语言文件
for (const file of localeFiles) {
  if (file === baseFile) continue;
  
  const currentKeys = getKeys(locales[file]).sort();
  const missingKeys = baseKeys.filter(key => !currentKeys.includes(key));
  const extraKeys = currentKeys.filter(key => !baseKeys.includes(key));
  
  if (missingKeys.length > 0 || extraKeys.length > 0) {
    hasErrors = true;
    console.log(`❌ ${file} - 键结构不一致`);
    
    if (missingKeys.length > 0) {
      console.log(`   缺失的键 (${missingKeys.length}):`);
      missingKeys.forEach(key => console.log(`   - ${key}`));
    }
    
    if (extraKeys.length > 0) {
      console.log(`   多余的键 (${extraKeys.length}):`);
      extraKeys.forEach(key => console.log(`   - ${key}`));
    }
    
    console.log('');
  } else {
    console.log(`✅ ${file} - 键结构一致 (${currentKeys.length} 个键)`);
  }
}

console.log('\n' + '='.repeat(60));

if (hasErrors) {
  console.log('❌ 验证失败: 发现键结构不一致的语言文件');
  console.log('请修复上述问题后重新验证');
  process.exit(1);
} else {
  console.log('✅ 验证通过: 所有语言文件键结构一致');
  console.log(`📊 共 ${localeFiles.length} 个语言文件, ${baseKeys.length} 个翻译键`);
  console.log('\n支持的语言:');
  localeFiles.forEach(file => {
    const locale = file.replace('.json', '');
    console.log(`  - ${locale}`);
  });
}

console.log('='.repeat(60) + '\n');
