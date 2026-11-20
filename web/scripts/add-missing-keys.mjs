import fs from 'node:fs/promises'
import path from 'node:path'

const LOCALES_DIR = path.resolve('src/i18n/locales')
const EVIDENCE =
  'C:/Users/林龙/Desktop/GitHub仓库修改/公共版本连接newapi/_evidence/gateway-auth-models-20261119-212337/public-admin-app'
const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']

function stableStringify(obj) {
  return JSON.stringify(obj, null, 2) + String.fromCharCode(10)
}

const newKeys = { en: {}, zh: {}, 'zh-TW': {}, fr: {}, ja: {}, ru: {}, vi: {} }
const originalByLocale = {}
for (const locale of locales) {
  const originalPath = path.join(
    EVIDENCE,
    'originals/web/src/i18n/locales',
    locale + '.json'
  )
  originalByLocale[locale] = JSON.parse(
    await fs.readFile(originalPath, 'utf8')
  ).translation
}

const rows = []
for (const filename of [
  'locale-admin-rows.json',
  'locale-auth-rows.json',
  'locale-model-rows.json',
  'locale-extra-rows.json',
  'locale-limit-rows.json',
]) {
  try {
    rows.push(
      ...JSON.parse(await fs.readFile(path.join(EVIDENCE, filename), 'utf8'))
    )
  } catch (error) {
    if (error.code !== 'ENOENT') throw error
  }
}
const placeholders = (value) =>
  (value.match(/{{[^}]+}}/g) ?? []).sort().join('|')
for (const row of rows) {
  if (row.length !== 7)
    throw new Error(
      'Each translation row requires a key and six language values'
    )
  const [key, ...translations] = row
  for (const [index, locale] of locales.entries()) {
    const value = locale === 'en' ? key : translations[index - 1]
    if (!value || placeholders(key) !== placeholders(value))
      throw new Error('Placeholder mismatch: ' + locale + ': ' + key)
    if (!Object.hasOwn(originalByLocale[locale], key))
      newKeys[locale][key] = value
  }
}
for (const [key, value] of Object.entries(
  JSON.parse(
    await fs.readFile(path.join(EVIDENCE, 'locale-tw-rows.json'), 'utf8')
  )
)) {
  if (!Object.hasOwn(originalByLocale['zh-TW'], key))
    newKeys['zh-TW'][key] = value
}

async function main() {
  let totalAdded = 0
  for (const [locale, trans] of Object.entries(newKeys)) {
    const filePath = path.join(LOCALES_DIR, locale + '.json')
    const json = JSON.parse(await fs.readFile(filePath, 'utf8'))
    let count = 0
    for (const [key, value] of Object.entries(trans)) {
      if (
        !Object.prototype.hasOwnProperty.call(json.translation, key) ||
        json.translation[key] !== value
      ) {
        json.translation[key] = value
        count++
      }
    }
    if (count > 0) {
      json.translation = Object.fromEntries(
        Object.entries(json.translation).sort(([a], [b]) => a.localeCompare(b))
      )
      await fs.writeFile(filePath, stableStringify(json), 'utf8')
    }
    console.log(locale + ': ' + count + ' translations applied')
    totalAdded += count
  }
  await fs.writeFile(
    path.join(EVIDENCE, 'locale-applied-keys.json'),
    stableStringify(newKeys),
    'utf8'
  )
  console.log('Total: ' + totalAdded + ' translations applied')
}
main().catch((error) => {
  console.error(error)
  process.exitCode = 1
})
