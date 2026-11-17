import { createFileRoute } from '@tanstack/react-router'

import { MyModelsPage } from '@/features/my-models'

export const Route = createFileRoute('/_authenticated/my-models/')({
  component: MyModelsPage,
})
