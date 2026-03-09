package agent

// Client Type
const (
	clientTypeBuyer   = "buyer"
	clientTypeSeller  = "seller"
	clientTypeLanlord = "landlord"
	clientTypeTenant  = "tenant"
)

// User Goal
const (
	userGoalStrongPriceGrowth   = "strong_price_growth"
	userGoalGoodRentalIncome    = "good_rental_income"
	userGoalConvenientLocation  = "convenient_location"
	userGoalSavingBudget        = "saving_budget"
	userGoalMaximumBudget       = "maximum_budget"
	userGoalLowPricePerSqft     = "low_price_per_sqft"
	userGoalTopPricePerSqft     = "top_price_per_sqft"
	userGoalSellingQuickly      = "selling_quickly"
	userGoalSavingRentalBudget  = "saving_rental_budget"
	userGoalMaximumRentalBudget = "maximum_rental_budget"
)

// Sort By
const (
	sortBySaleTransactionAVGAnnualProfitLossPercent = "saleTransactionAVGAnnualProfitLossPercent"
	sortByAvgRentalYieldPercent                     = "avgRentalYieldPercent"
	sortBySaleListingMaxPrice                       = "saleListingMaxPrice"
	sortBySaleListingMaxPSF                         = "saleListingMaxPSF"
	sortBySaleListingVolume                         = "saleListingVolume"
	sortByRentalTransactionMaxPrice                 = "rentalTransactionMaxPrice"

	// For NewLaunch
	sortByAvailableUnitVolume   = "availableUnitVolume"
	sortByAvailableUnitMaxPrice = "availableUnitMaxPrice"
	sortByAvailableUnitMaxPSF   = "availableUnitMaxPSF"

	sortByAmenitiesCount = "amenitiesCount"
)

// Sort Order
const (
	sortOrderASC  = "ASC"
	sortOrderDESC = "DESC"
)

// clientTypeToUserGoalQuestion returns the question and available goals for a given clientType.
func clientTypeToUserGoalQuestion(clientType string) (question string, goals []string) {
	switch clientType {
	case clientTypeBuyer:
		return "As a buyer, what matters most to you: strong price growth, good rental income, convenient location, saving budget, or low price per square foot?",
			[]string{
				userGoalStrongPriceGrowth,
				userGoalGoodRentalIncome,
				userGoalConvenientLocation,
				userGoalSavingBudget,
				userGoalLowPricePerSqft,
			}
	case clientTypeSeller:
		return "As a seller, what is your priority: selling quickly, top price per square foot, or maximum sale price?",
			[]string{
				userGoalSellingQuickly,
				userGoalTopPricePerSqft,
				userGoalMaximumBudget,
			}
	case clientTypeTenant:
		return "As a tenant, what matters most to you: saving rental budget, maximum rental budget, or convenient location?",
			[]string{
				userGoalSavingRentalBudget,
				userGoalMaximumRentalBudget,
				userGoalConvenientLocation,
			}
	case clientTypeLanlord:
		return "As a landlord, what is your goal: good rental income, maximum rental price, or convenient location?",
			[]string{
				userGoalGoodRentalIncome,
				userGoalMaximumRentalBudget,
				userGoalConvenientLocation,
			}
	}
	return "What is your main goal?", []string{}
}

func userGoalToSortMetrics(userGoal string) (sortBy, sortOrder string) {
	switch userGoal {
	case userGoalStrongPriceGrowth:
		return sortBySaleTransactionAVGAnnualProfitLossPercent, sortOrderDESC
	case userGoalGoodRentalIncome:
		return sortByAvgRentalYieldPercent, sortOrderDESC
	case userGoalConvenientLocation:
		return sortByAmenitiesCount, sortOrderDESC
	case userGoalSavingBudget:
		return sortBySaleListingMaxPrice, sortOrderASC
	case userGoalMaximumBudget:
		return sortBySaleListingMaxPrice, sortOrderDESC
	case userGoalLowPricePerSqft:
		return sortBySaleListingMaxPSF, sortOrderASC
	case userGoalTopPricePerSqft:
		return sortBySaleListingMaxPSF, sortOrderDESC
	case userGoalSellingQuickly:
		return sortBySaleListingVolume, sortOrderDESC
	case userGoalSavingRentalBudget:
		return sortByRentalTransactionMaxPrice, sortOrderASC
	case userGoalMaximumRentalBudget:
		return sortByRentalTransactionMaxPrice, sortOrderDESC
	}

	return "", ""
}
