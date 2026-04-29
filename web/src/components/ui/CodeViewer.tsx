import { type ComponentProps } from 'react'
import { CodeViewer as BaseCodeViewer } from '@skyhook-io/k8s-ui'
import { useTheme } from '../../context/ThemeContext'

export function CodeViewer(props: Omit<ComponentProps<typeof BaseCodeViewer>, 'theme'>) {
  // BaseCodeViewer wants a concrete dark/light value, not the user's
  // stored preference (which can now be 'system'). Pass the resolved
  // effective theme.
  const { effectiveTheme } = useTheme()
  return <BaseCodeViewer {...props} theme={effectiveTheme} />
}
