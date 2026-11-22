const PROVIDER_PREFIXES = ['openai', 'anthropic', 'google', 'cohere', 'mistral', 'meta']

export function shortModelName(model: string): string {
  if (!model) return model
  const slashParts = model.split('/')
  let name = slashParts[slashParts.length - 1]

  const dotParts = name.split('.')
  if (dotParts.length > 1 && PROVIDER_PREFIXES.includes(dotParts[0].toLowerCase())) {
    name = dotParts.slice(1).join('.')
  }
  return name
}
