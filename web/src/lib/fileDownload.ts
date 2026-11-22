export type FileBlobFetcher = {
  fileBlob: (path: string, signal?: AbortSignal) => Promise<Blob>
}

export function triggerBlobDownload(blob: Blob, name?: string): void {
  const url = URL.createObjectURL(blob)
  try {
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = name ?? 'download'
    anchor.style.display = 'none'
    document.body.appendChild(anchor)
    anchor.click()
    anchor.remove()
  } finally {
    URL.revokeObjectURL(url)
  }
}

export async function downloadFileViaApi(
  fetcher: FileBlobFetcher,
  path: string,
  name?: string,
): Promise<void> {
  triggerBlobDownload(await fetcher.fileBlob(path), name)
}
