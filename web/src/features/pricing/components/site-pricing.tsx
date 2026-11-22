/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { useCallback, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'

import { EXCLUDED_GROUPS, VIEW_MODES } from '../constants'
import { useFilters } from '../hooks/use-filters'
import { usePricingData } from '../hooks/use-pricing-data'
import { EmptyState } from './empty-state'
import { LoadingSkeleton } from './loading-skeleton'
import { ModelCardGrid } from './model-card-grid'
import { ModelDetailsDrawer } from './model-details'
import { PricingSidebar } from './pricing-sidebar'
import { PricingTable } from './pricing-table'
import { PricingToolbar } from './pricing-toolbar'
import { SearchBar } from './search-bar'

export function SitePricing(props: { active: boolean }) {
  const { t } = useTranslation()
  const [selectedModelName, setSelectedModelName] = useState<string | null>(
    null
  )
  const {
    models,
    vendors,
    groupRatio,
    usableGroup,
    endpointMap,
    autoGroups,
    isLoading,
    error,
    refetch,
    priceRate,
    usdExchangeRate,
  } = usePricingData(props.active)
  const {
    searchInput,
    sortBy,
    vendorFilter,
    groupFilter,
    quotaTypeFilter,
    endpointTypeFilter,
    tagFilter,
    tokenUnit,
    viewMode,
    showRechargePrice,
    setSearchInput,
    setSortBy,
    setVendorFilter,
    setGroupFilter,
    setQuotaTypeFilter,
    setEndpointTypeFilter,
    setTagFilter,
    setTokenUnit,
    setViewMode,
    setShowRechargePrice,
    filteredModels,
    hasActiveFilters,
    activeFilterCount,
    availableTags,
    clearFilters,
    clearSearch,
  } = useFilters(models)

  const selectedModel = useMemo(
    () => models.find((model) => model.model_name === selectedModelName),
    [models, selectedModelName]
  )
  const availableGroups = useMemo(
    () => Object.keys(usableGroup).filter((g) => !EXCLUDED_GROUPS.includes(g)),
    [usableGroup]
  )
  const handleClearAll = useCallback(() => {
    clearFilters()
    clearSearch()
  }, [clearFilters, clearSearch])

  // Keep site filters when switching sources, but don't mount its controls or drawer while hidden.
  if (!props.active) return null
  if (isLoading) {
    return (
      <div role='status' aria-label={t('Loading models...')} aria-busy='true'>
        <LoadingSkeleton viewMode={viewMode} />
      </div>
    )
  }
  if (error && models.length === 0) {
    return (
      <div role='alert'>
        <ErrorState
          title={t('Failed to load model pricing')}
          onRetry={() => void refetch()}
        />
      </div>
    )
  }

  const filters = {
    quotaTypeFilter,
    endpointTypeFilter,
    vendorFilter,
    groupFilter,
    tagFilter,
    onQuotaTypeChange: setQuotaTypeFilter,
    onEndpointTypeChange: setEndpointTypeFilter,
    onVendorChange: setVendorFilter,
    onGroupChange: setGroupFilter,
    onTagChange: setTagFilter,
    vendors,
    groups: availableGroups,
    groupRatios: groupRatio,
    tags: availableTags,
    models,
    hasActiveFilters,
    onClearFilters: clearFilters,
  }
  const modelView = {
    models: filteredModels,
    priceRate,
    usdExchangeRate,
    tokenUnit,
    showRechargePrice,
    selectedGroup: groupFilter,
    onModelClick: setSelectedModelName,
  }
  let content
  if (filteredModels.length === 0) {
    content = (
      <EmptyState
        searchQuery={searchInput}
        hasActiveFilters={hasActiveFilters}
        onClearFilters={handleClearAll}
      />
    )
  } else if (viewMode === VIEW_MODES.CARD) {
    content = <ModelCardGrid {...modelView} />
  } else {
    content = <PricingTable {...modelView} />
  }

  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <div className='mx-auto flex w-full max-w-2xl flex-col gap-3 text-center'>
        <p className='text-muted-foreground text-sm sm:text-base'>
          {t('This site currently has {{count}} models enabled', {
            count: models.length,
          })}
        </p>
        <SearchBar
          value={searchInput}
          onChange={setSearchInput}
          onClear={clearSearch}
          placeholder={t('Search model name, provider, endpoint, or tag...')}
        />
      </div>
      <div className='grid gap-4 xl:grid-cols-[280px_minmax(0,1fr)]'>
        <PricingSidebar
          {...filters}
          className='hover-scrollbar sticky top-20 hidden max-h-[calc(100dvh-6rem)] self-start overflow-y-auto xl:block'
        />
        <div className='flex min-w-0 flex-col gap-4'>
          <PricingToolbar
            {...filters}
            filteredCount={filteredModels.length}
            totalCount={models.length}
            sortBy={sortBy}
            onSortChange={setSortBy}
            tokenUnit={tokenUnit}
            onTokenUnitChange={setTokenUnit}
            showRechargePrice={showRechargePrice}
            onRechargePriceChange={setShowRechargePrice}
            viewMode={viewMode}
            onViewModeChange={setViewMode}
            activeFilterCount={activeFilterCount}
          />
          {content}
        </div>
      </div>
      {selectedModel && (
        <ModelDetailsDrawer
          open
          onOpenChange={(open) => {
            if (!open) setSelectedModelName(null)
          }}
          model={selectedModel}
          groupRatio={groupRatio}
          usableGroup={usableGroup}
          endpointMap={
            endpointMap as Record<string, { path?: string; method?: string }>
          }
          autoGroups={autoGroups}
          priceRate={priceRate}
          usdExchangeRate={usdExchangeRate}
          tokenUnit={tokenUnit}
          showRechargePrice={showRechargePrice}
        />
      )}
    </div>
  )
}
