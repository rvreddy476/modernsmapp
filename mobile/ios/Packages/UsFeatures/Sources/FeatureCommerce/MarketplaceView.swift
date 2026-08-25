import SwiftUI
import UsModel
import UsDesignSystem
import UsNetwork

@Observable
public final class MarketplaceViewModel: @unchecked Sendable {
    public var products: [Product] = []
    public var selectedCategory: String = "All"
    public var searchQuery: String = ""
    public var cartItems: [CartItem] = []
    public var isLoading: Bool = false

    private let client: APIClientProtocol
    public let categories = ["All", "Fashion", "Tech", "Home", "Beauty", "Sneakers"]

    public init(client: APIClientProtocol = APIClient()) {
        self.client = client
        populateMockProducts()
    }

    public var filteredProducts: [Product] {
        products.filter { p in
            (selectedCategory == "All" || p.category == selectedCategory) &&
            (searchQuery.isEmpty || p.title.localizedCaseInsensitiveContains(searchQuery))
        }
    }

    public func addToCart(product: Product) {
        if let idx = cartItems.firstIndex(where: { $0.product.id == product.id }) {
            cartItems[idx].quantity += 1
        } else {
            cartItems.append(CartItem(product: product, quantity: 1))
        }
        ToastManager.shared.show("Added to Cart", style: .success)
    }

    private func populateMockProducts() {
        products = [
            Product(id: "p1", title: "Wireless Noise Cancelling Earbuds", description: "Hi-Res Audio with 36hr battery life.", pricePaise: 499900, formattedPrice: "₹4,999", originalPricePaise: 899900, formattedOriginalPrice: "₹8,999", imageUrls: ["https://images.unsplash.com/photo-1590658268037-6bf12165a8df?w=600"], category: "Tech"),
            Product(id: "p2", title: "Minimalist Oversized Graphic Hoodie", description: "Heavyweight 400 GSM French Terry cotton.", pricePaise: 249900, formattedPrice: "₹2,499", originalPricePaise: 399900, formattedOriginalPrice: "₹3,999", imageUrls: ["https://images.unsplash.com/photo-1556905055-8f358a7a47b2?w=600"], category: "Fashion"),
            Product(id: "p3", title: "Ceramic Drip Coffee Maker", description: "Handcrafted matte black pour-over brewer.", pricePaise: 189900, formattedPrice: "₹1,899", originalPricePaise: 249900, formattedOriginalPrice: "₹2,499", imageUrls: ["https://images.unsplash.com/photo-1514432324607-a09d9b4aefdd?w=600"], category: "Home"),
            Product(id: "p4", title: "Retro Runner Sneakers (Cloud Grey)", description: "Ultra-cushioned responsive everyday sneaker.", pricePaise: 649900, formattedPrice: "₹6,499", originalPricePaise: 999900, formattedOriginalPrice: "₹9,999", imageUrls: ["https://images.unsplash.com/photo-1542291026-7eec264c27ff?w=600"], category: "Sneakers")
        ]
    }
}

public struct MarketplaceView: View {
    @State private var viewModel = MarketplaceViewModel()
    @State private var showCartSheet: Bool = false
    public let onOpenProduct: (Product) -> Void

    public init(onOpenProduct: @escaping (Product) -> Void = { _ in }) {
        self.onOpenProduct = onOpenProduct
    }

    private let columns = [
        GridItem(.flexible(), spacing: 14),
        GridItem(.flexible(), spacing: 14)
    ]

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        // Search Bar
                        searchField

                        // Categories Pill Filter
                        categoryFilters

                        // Products Grid
                        LazyVGrid(columns: columns, spacing: 16) {
                            ForEach(viewModel.filteredProducts) { product in
                                productCard(product)
                            }
                        }
                    }
                    .padding(16)
                }
            }
            .navigationTitle("Shop")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button(action: { showCartSheet = true }) {
                        ZStack(alignment: .topTrailing) {
                            Image(systemName: "bag")
                                .font(.system(size: 20))
                                .foregroundColor(UsColors.textPrimary)

                            if !viewModel.cartItems.isEmpty {
                                Text("\(viewModel.cartItems.count)")
                                    .font(.system(size: 10, weight: .bold))
                                    .foregroundColor(.white)
                                    .padding(4)
                                    .background(UsColors.postgramPrimary)
                                    .clipShape(Circle())
                                    .offset(x: 8, y: -6)
                            }
                        }
                    }
                }
            }
            .sheet(isPresented: $showCartSheet) {
                CartView(cartItems: $viewModel.cartItems) {
                    showCartSheet = false
                }
            }
        }
    }

    private var searchField: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(UsColors.textMuted)
            TextField("Search fashion, tech, decor...", text: $viewModel.searchQuery)
                .textFieldStyle(.plain)
                .font(.system(size: 14))
                .foregroundColor(UsColors.textPrimary)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }

    private var categoryFilters: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(viewModel.categories, id: \.self) { cat in
                    let isSelected = viewModel.selectedCategory == cat
                    Button(action: { viewModel.selectedCategory = cat }) {
                        Text(cat)
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
    private func productCard(_ product: Product) -> some View {
        Button(action: { onOpenProduct(product) }) {
            VStack(alignment: .leading, spacing: 8) {
                ZStack(alignment: .bottomTrailing) {
                    if let img = product.imageUrls.first, let url = URL(string: img) {
                        AsyncImage(url: url) { phase in
                            switch phase {
                            case .success(let image):
                                image.resizable().scaledToFill()
                            default:
                                Rectangle().fill(UsColors.bgTertiary)
                            }
                        }
                    } else {
                        Rectangle().fill(UsColors.bgTertiary)
                    }

                    Button(action: { viewModel.addToCart(product: product) }) {
                        Image(systemName: "plus")
                            .font(.system(size: 14, weight: .bold))
                            .foregroundColor(.black)
                            .padding(8)
                            .background(Color.white)
                            .clipShape(Circle())
                            .shadow(radius: 4)
                    }
                    .padding(8)
                }
                .frame(height: 160)
                .clipShape(RoundedRectangle(cornerRadius: 12))

                VStack(alignment: .leading, spacing: 2) {
                    Text(product.title)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(UsColors.textPrimary)
                        .lineLimit(2)

                    HStack(spacing: 6) {
                        Text(product.formattedPrice)
                            .font(.system(size: 15, weight: .bold, design: .rounded))
                            .foregroundColor(UsColors.textPrimary)

                        if let orig = product.formattedOriginalPrice {
                            Text(orig)
                                .font(.system(size: 12))
                                .strikethrough()
                                .foregroundColor(UsColors.textDim)
                        }
                    }
                }
            }
        }
        .buttonStyle(.plain)
    }
}

public struct CartView: View {
    @Binding public var cartItems: [CartItem]
    @State private var isCheckingOut: Bool = false
    public let onDismiss: () -> Void

    public init(cartItems: Binding<[CartItem]>, onDismiss: @escaping () -> Void = {}) {
        self._cartItems = cartItems
        self.onDismiss = onDismiss
    }

    private var totalPaise: Int64 {
        cartItems.reduce(0) { $0 + ($1.product.pricePaise * Int64($1.quantity)) }
    }

    private var formattedTotal: String {
        let rupees = Double(totalPaise) / 100.0
        return String(format: "₹%.2f", rupees)
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                if cartItems.isEmpty {
                    UsEmptyState(title: "Your Cart is Empty", detail: "Discover unique products and add them here.")
                } else {
                    VStack {
                        List {
                            ForEach($cartItems) { $item in
                                HStack(spacing: 12) {
                                    VStack(alignment: .leading, spacing: 4) {
                                        Text(item.product.title)
                                            .font(.system(size: 14, weight: .semibold))
                                            .foregroundColor(UsColors.textPrimary)
                                        Text(item.product.formattedPrice)
                                            .font(.system(size: 13, weight: .bold))
                                            .foregroundColor(UsColors.postbookPrimary)
                                    }

                                    Spacer()

                                    HStack(spacing: 8) {
                                        Button(action: {
                                            if item.quantity > 1 { item.quantity -= 1 }
                                            else { cartItems.removeAll { $0.id == item.id } }
                                        }) {
                                            Image(systemName: "minus.circle")
                                                .foregroundColor(UsColors.textMuted)
                                        }

                                        Text("\(item.quantity)")
                                            .font(.system(size: 14, weight: .bold))
                                            .foregroundColor(UsColors.textPrimary)

                                        Button(action: { item.quantity += 1 }) {
                                            Image(systemName: "plus.circle")
                                                .foregroundColor(UsColors.textMuted)
                                        }
                                    }
                                }
                                .listRowBackground(UsColors.bgSecondary)
                            }
                        }
                        .listStyle(.plain)
                        .scrollContentBackground(.hidden)

                        // Checkout Summary Bar
                        VStack(spacing: 12) {
                            HStack {
                                Text("Total Amount")
                                    .foregroundColor(UsColors.textMuted)
                                Spacer()
                                Text(formattedTotal)
                                    .font(.system(size: 20, weight: .bold, design: .rounded))
                                    .foregroundColor(UsColors.textPrimary)
                            }

                            Button(action: {
                                isCheckingOut = true
                                DispatchQueue.main.asyncAfter(deadline: .now() + 1.2) {
                                    isCheckingOut = false
                                    cartItems.removeAll()
                                    ToastManager.shared.show("Order Placed Successfully via UPI", style: .success)
                                    onDismiss()
                                }
                            }) {
                                HStack {
                                    Spacer()
                                    if isCheckingOut {
                                        ProgressView().tint(.black)
                                    } else {
                                        Text("Pay with US Wallet / UPI")
                                            .font(.system(size: 16, weight: .bold))
                                            .foregroundColor(.black)
                                    }
                                    Spacer()
                                }
                                .padding(.vertical, 16)
                                .background(Color.white)
                                .clipShape(RoundedRectangle(cornerRadius: 14))
                            }
                            .disabled(isCheckingOut)
                        }
                        .padding(16)
                        .background(UsColors.bgSecondary)
                    }
                }
            }
            .navigationTitle("Shopping Bag")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close", action: onDismiss)
                        .foregroundColor(UsColors.textMuted)
                }
            }
        }
    }
}
