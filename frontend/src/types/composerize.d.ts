declare module 'composerize' {
  function composerize(
    dockerRunCommand: string,
    existingCompose?: string | null,
    composeVersion?: 'v2x' | 'v3x' | 'latest',
    indent?: number,
  ): string

  export default composerize
}
