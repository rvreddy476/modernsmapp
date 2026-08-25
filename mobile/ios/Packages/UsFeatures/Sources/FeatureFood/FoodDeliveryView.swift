import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class FoodDeliveryViewModel: @unchecked Sendable {
    public var restaurants: [Restaurant] = []
    public var selectedCuisine: String = "All"
    public var searchQuery: String = ""
    public var isLoading: Bool = false

    private let client: APIClientProtocol
    public let cuisines = ["All", "Biryani", "Pizza", "South Indian", "Burgers", "Desserts", "Chinese"]

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        populateMockRestaurants()
    }

    public var filteredRestaurants: [Restaurant] {
        restaurants.filter { r in
            (selectedCuisine == "All" || r.cuisine.contains(selectedCuisine)) &&
            (searchQuery.isEmpty || r.name.localizedCaseInsensitiveContains(searchQuery))
        }
    }

    @MainActor
    public func loadRestaurants() async {
        isLoading = true
        do {
            let res: [Restaurant] = try await client.request(
                endpoint: "v1/food/restaurants",
                method: "GET",
                query: nil,
                body: nil
            )
            if !res.isEmpty {
                self.restaurants = res
            }
        } catch {
            // Keep default curated restaurant set if offline/endpoint cold
        }
        isLoading = false
    }

    private func populateMockRestaurants() {
        restaurants = [
            Restaurant(id: "r1", name: "Meghana Foods", cuisine: "Biryani, Andhra, Seafood", rating: 4.7, deliveryTimeMins: 25, priceForTwo: "₹500 for two", imageUrl: "https://images.unsplash.com/photo-1563379091339-03b21ab4a4f8?w=800"),
            Restaurant(id: "r2", name: "Toscano Artisanal Pizza", cuisine: "Italian, Sourdough Pizza, Pasta", rating: 4.8, deliveryTimeMins: 35, priceForTwo: "₹800 for two", imageUrl: "https://images.unsplash.com/photo-1513104890138-7c749659a591?w=800"),
            Restaurant(id: "r3", name: "Rameshwaram Cafe", cuisine: "South Indian, Ghee Roast, Filter Coffee", rating: 4.9, deliveryTimeMins: 20, priceForTwo: "₹250 for two", imageUrl: "https://images.unsplash.com/photo-1589301760014-d929f3979dbc?w=800", isPureVeg: true),
            Restaurant(id: "r4", name: "The Burger Club", cuisine: "American Gourmet Burgers & Shakes", rating: 4.5, deliveryTimeMins: 30, priceForTwo: "₹450 for two", imageUrl: "https://images.unsplash.com/photo-1568901346375-23c9450c58cd?w=800")
        ]
    }
}

public struct FoodDeliveryView: View {
    @State private var viewModel: FoodDeliveryViewModel
    @State private var selectedRestaurant: Restaurant? = nil

    public init(client: APIClientProtocol = APIClient()) {
        _viewModel = State(initialValue: FoodDeliveryViewModel(client: client))
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 18) {
                        // Search Bar
                        searchField

                        // Cuisine Filters
                        cuisineFilterRow

                        // Featured Restaurants
                        Text("Top Restaurants Nearby")
                            .font(.system(size: 17, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 16) {
                            ForEach(viewModel.filteredRestaurants) { restaurant in
                                restaurantCard(restaurant)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Food Delivery")
            .task {
                await viewModel.loadRestaurants()
            }
            .sheet(item: $selectedRestaurant) { rest in
                RestaurantDetailView(restaurant: rest) {
                    selectedRestaurant = nil
                }
            }
        }
    }

    private var searchField: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(UsColors.textMuted)
            TextField("Search dishes, restaurants...", text: $viewModel.searchQuery)
                .textFieldStyle(.plain)
                .font(.system(size: 14))
                .foregroundColor(UsColors.textPrimary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }

    private var cuisineFilterRow: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(viewModel.cuisines, id: \.self) { cuisine in
                    let isSelected = viewModel.selectedCuisine == cuisine
                    Button(action: { viewModel.selectedCuisine = cuisine }) {
                        Text(cuisine)
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(isSelected ? .black : UsColors.textPrimary)
                            .padding(.horizontal, 16)
                            .padding(.vertical, 8)
                            .background(isSelected ? Color.white : UsColors.bgSecondary)
                            .clipShape(Capsule())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    @ViewBuilder
    private func restaurantCard(_ restaurant: Restaurant) -> some View {
        Button(action: { selectedRestaurant = restaurant }) {
            VStack(alignment: .leading, spacing: 10) {
                ZStack(alignment: .bottomLeading) {
                    if let url = URL(string: restaurant.imageUrl) {
                        AsyncImage(url: url) { phase in
                            switch phase {
                            case .success(let img):
                                img.resizable().scaledToFill()
                            default:
                                Rectangle().fill(UsColors.bgTertiary)
                            }
                        }
                    } else {
                        Rectangle().fill(UsColors.bgTertiary)
                    }

                    // Delivery time pill
                    HStack(spacing: 4) {
                        Image(systemName: "clock.fill")
                            .font(.system(size: 10))
                        Text("\(restaurant.deliveryTimeMins) mins")
                            .font(.system(size: 11, weight: .bold))
                    }
                    .foregroundColor(.white)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(Color.black.opacity(0.75))
                    .clipShape(Capsule())
                    .padding(10)
                }
                .frame(height: 160)
                .clipShape(RoundedRectangle(cornerRadius: 14))

                // Info row
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(restaurant.name)
                            .font(.system(size: 16, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        Text(restaurant.cuisine)
                            .font(.system(size: 12))
                            .foregroundColor(UsColors.textMuted)
                            .lineLimit(1)
                    }

                    Spacer()

                    HStack(spacing: 4) {
                        Image(systemName: "star.fill")
                            .font(.system(size: 12))
                            .foregroundColor(.white)
                        Text(String(format: "%.1f", restaurant.rating))
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(.white)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(UsColors.onlineGreen)
                    .clipShape(RoundedRectangle(cornerRadius: 6))
                }
            }
            .padding(12)
            .background(UsColors.bgSecondary)
            .clipShape(RoundedRectangle(cornerRadius: 18))
        }
        .buttonStyle(.plain)
    }
}

public struct RestaurantDetailView: View {
    public let restaurant: Restaurant
    public let onDismiss: () -> Void

    @State private var items: [FoodMenuItem] = [
        FoodMenuItem(id: "m1", name: "Special Chicken Biryani", description: "Aromatic basmati rice cooked with marinated chicken and Andhra spices.", pricePaise: 36000, formattedPrice: "₹360", isVeg: false, isBestseller: true),
        FoodMenuItem(id: "m2", name: "Paneer Butter Masala", description: "Cottage cheese cubes in rich tomato cashew gravy.", pricePaise: 28000, formattedPrice: "₹280", isVeg: true, isBestseller: false),
        FoodMenuItem(id: "m3", name: "Butter Garlic Naan", description: "Crisp tandoori naan brushed with fresh garlic & melted butter.", pricePaise: 6500, formattedPrice: "₹65", isVeg: true, isBestseller: true)
    ]
    @State private var cartCount: Int = 0

    public init(restaurant: Restaurant, onDismiss: @escaping () -> Void = {}) {
        self.restaurant = restaurant
        self.onDismiss = onDismiss
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        // Header info
                        VStack(alignment: .leading, spacing: 4) {
                            Text(restaurant.name)
                                .font(.system(size: 22, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)
                            Text(restaurant.cuisine)
                                .font(.system(size: 13))
                                .foregroundColor(UsColors.textMuted)
                            Text("\(restaurant.deliveryTimeMins) mins • \(restaurant.priceForTwo)")
                                .font(.system(size: 12, weight: .semibold))
                                .foregroundColor(UsColors.postbookPrimary)
                        }

                        Divider().background(UsColors.borderSubtle)

                        Text("Recommended Menu")
                            .font(.system(size: 17, weight: .bold))
                            .foregroundColor(UsColors.textPrimary)

                        LazyVStack(spacing: 12) {
                            ForEach(items) { item in
                                menuItemRow(item)
                            }
                        }
                    }
                    .padding(16)
                }

                if cartCount > 0 {
                    VStack {
                        Spacer()
                        Button(action: {
                            ToastManager.shared.show("Food Order Placed Successfully via UPI", style: .success)
                            onDismiss()
                        }) {
                            HStack {
                                Text("\(cartCount) items added")
                                    .font(.system(size: 14, weight: .semibold))
                                Spacer()
                                Text("View Cart & Pay")
                                    .font(.system(size: 15, weight: .bold))
                            }
                            .foregroundColor(.black)
                            .padding(.horizontal, 20)
                            .padding(.vertical, 16)
                            .background(Color.white)
                            .clipShape(RoundedRectangle(cornerRadius: 16))
                            .shadow(radius: 10)
                        }
                        .padding(16)
                    }
                }
            }
            .navigationTitle(restaurant.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }

    @ViewBuilder
    private func menuItemRow(_ item: FoodMenuItem) -> some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Circle()
                        .fill(item.isVeg ? UsColors.onlineGreen : UsColors.statusError)
                        .frame(width: 8, height: 8)

                    if item.isBestseller {
                        Text("BESTSELLER")
                            .font(.system(size: 10, weight: .black))
                            .foregroundColor(Color.orange)
                    }
                }

                Text(item.name)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundColor(UsColors.textPrimary)

                Text(item.formattedPrice)
                    .font(.system(size: 14, weight: .bold, design: .rounded))
                    .foregroundColor(UsColors.textPrimary)

                Text(item.description)
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textMuted)
                    .lineLimit(2)
            }

            Spacer()

            Button(action: {
                cartCount += 1
                ToastManager.shared.show("Added to Order", style: .success)
            }) {
                Text("ADD")
                    .font(.system(size: 14, weight: .black))
                    .foregroundColor(UsColors.onlineGreen)
                    .padding(.horizontal, 20)
                    .padding(.vertical, 8)
                    .background(UsColors.onlineGreen.opacity(0.15))
                    .clipShape(RoundedRectangle(cornerRadius: 8))
                    .overlay(RoundedRectangle(cornerRadius: 8).stroke(UsColors.onlineGreen, lineWidth: 1))
            }
        }
        .padding(12)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }
}
