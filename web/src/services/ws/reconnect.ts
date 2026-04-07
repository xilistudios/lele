export const initialReconnectDelay = 500
export const maxReconnectDelay = 5000

export const nextReconnectDelay = (current: number) => Math.min(current * 2, maxReconnectDelay)
