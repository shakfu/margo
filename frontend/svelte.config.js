import sveltePreprocess from 'svelte-preprocess'
import { preprocessMeltUI } from '@melt-ui/pp'
import sequence from 'svelte-sequential-preprocessor'

export default {
  preprocess: sequence([sveltePreprocess(), preprocessMeltUI()])
}
