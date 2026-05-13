const DEBUG_WS = (() => {
  try {
    const flag = localStorage.getItem('debug')
    return flag ? flag.split(',').includes('ws') : false
  } catch {
    return false
  }
})()

export function wsDebug(...args: unknown[]) {
  if (DEBUG_WS) {
    console.log(...args)
  }
}

export function wsWarn(...args: unknown[]) {
  // Warnings are always shown
  console.warn(...args)
}
