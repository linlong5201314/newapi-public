import fs from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'

const LOCALES_DIR = path.resolve('src/i18n/locales')
const locales = ['en', 'zh', 'zh-TW', 'fr', 'ja', 'ru', 'vi']
const rows = JSON.parse(await fs.readFile(process.argv[2], 'utf8'))
for (const [index, locale] of locales.entries()) {
  const file = path.join(LOCALES_DIR, locale + '.json')
  const json = JSON.parse(await fs.readFile(file, 'utf8'))
  let count = 0
  for (const row of rows) {
    if (row.length !== locales.length)
      throw new Error('Translation row must contain a key and six translations')
    const [key, ...values] = row
    const value = locale === 'en' ? key : values[index - 1]
    if (!value) throw new Error(`Missing ${locale} translation: ${key}`)
    if (json.translation[key] !== value) {
      json.translation[key] = value
      count++
    }
  }
  json.translation = Object.fromEntries(
    Object.entries(json.translation).sort(([a], [b]) => a.localeCompare(b))
  )
  await fs.writeFile(file, JSON.stringify(json, null, 2) + '\n')
  console.log(`${locale}: ${count} translations applied`)
}
