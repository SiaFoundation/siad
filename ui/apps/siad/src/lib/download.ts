export async function downloadJsonFile(
  name: string,
  data: Record<string, unknown>
) {
  const fileName = name
  const json = JSON.stringify(data)
  const blob = new Blob([json], { type: 'application/json' })
  const href = await URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = href
  link.download = fileName + '.json'
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
}
