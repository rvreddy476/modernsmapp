import SwiftUI
import UsDesignSystem
import UsNetwork

public struct AuthView: View {
    @State private var viewModel: AuthViewModel
    @State private var isRegisterMode: Bool = false

    public init(client: APIClientProtocol = APIClient()) {
        _viewModel = State(initialValue: AuthViewModel(client: client))
    }

    public var body: some View {
        NavigationStack {
            ZStack {
                UsColors.bgPrimary
                    .ignoresSafeArea()

                ScrollView {
                    VStack(spacing: 24) {
                        // Brand Logo / Header
                        VStack(spacing: 8) {
                            Text("US")
                                .font(.system(size: 42, weight: .black, design: .rounded))
                                .foregroundStyle(
                                    LinearGradient(
                                        colors: [UsColors.postbookPrimary, UsColors.postgramPrimary],
                                        startPoint: .leading,
                                        endPoint: .trailing
                                    )
                                )

                            Text(viewModel.needsOTP ? "Verify Email" : (isRegisterMode ? "Create an Account" : "Welcome Back"))
                                .font(.system(size: 20, weight: .bold))
                                .foregroundColor(UsColors.textPrimary)

                            Text(viewModel.needsOTP ? "Enter the 6-digit verification code sent to your email." : "Connect, share, and discover.")
                                .font(.system(size: 14))
                                .foregroundColor(UsColors.textMuted)
                        }
                        .padding(.top, 30)

                        if let error = viewModel.errorMessage {
                            Text(error)
                                .font(.system(size: 13, weight: .medium))
                                .foregroundColor(UsColors.statusError)
                                .multilineTextAlignment(.center)
                                .padding(.horizontal)
                        }

                        if let success = viewModel.successMessage {
                            Text(success)
                                .font(.system(size: 13, weight: .medium))
                                .foregroundColor(UsColors.statusSuccess)
                                .multilineTextAlignment(.center)
                                .padding(.horizontal)
                        }

                        // Form Inputs
                        if viewModel.needsOTP {
                            otpForm
                        } else if isRegisterMode {
                            registerForm
                        } else {
                            loginForm
                        }

                        // Mode switcher
                        if !viewModel.needsOTP {
                            Button(action: {
                                isRegisterMode.toggle()
                                viewModel.errorMessage = nil
                                viewModel.successMessage = nil
                            }) {
                                Text(isRegisterMode ? "Already have an account? Sign In" : "Don't have an account? Sign Up")
                                    .font(.system(size: 14, weight: .semibold))
                                    .foregroundColor(UsColors.postbookPrimary)
                            }
                            .padding(.top, 8)
                        }
                    }
                    .padding(24)
                }
            }
        }
    }

    private var loginForm: some View {
        VStack(spacing: 16) {
            customField(placeholder: "Email or username", text: $viewModel.identifier)
            customField(placeholder: "Password", text: $viewModel.password, isSecure: true)

            primaryButton(title: "Sign In") {
                Task { await viewModel.login() }
            }
        }
    }

    private var registerForm: some View {
        VStack(spacing: 14) {
            HStack(spacing: 10) {
                customField(placeholder: "First Name", text: $viewModel.firstName)
                customField(placeholder: "Last Name", text: $viewModel.lastName)
            }

            customField(placeholder: "Username", text: $viewModel.username)
            customField(placeholder: "Display Name (optional)", text: $viewModel.displayName)
            customField(placeholder: "Email", text: $viewModel.identifier)
            customField(placeholder: "Password", text: $viewModel.password, isSecure: true)

            HStack(spacing: 12) {
                Text("Gender:")
                    .font(.system(size: 14))
                    .foregroundColor(UsColors.textMuted)

                Picker("Gender", selection: $viewModel.gender) {
                    Text("Female").tag("female")
                    Text("Male").tag("male")
                    Text("Other").tag("other")
                }
                .pickerStyle(.segmented)
            }
            .padding(.horizontal, 4)

            HStack {
                Text("Date of Birth (YYYY-MM-DD):")
                    .font(.system(size: 13))
                    .foregroundColor(UsColors.textMuted)
                Spacer()
            }
            customField(placeholder: "1998-05-15", text: $viewModel.dob)

            Toggle(isOn: $viewModel.acceptedTerms) {
                Text("I accept the Terms of Service & Privacy Policy")
                    .font(.system(size: 12))
                    .foregroundColor(UsColors.textSecondary)
            }
            .tint(UsColors.postbookPrimary)
            .padding(.top, 4)

            primaryButton(title: "Sign Up") {
                Task { await viewModel.register() }
            }
            .disabled(!viewModel.acceptedTerms)
        }
    }

    private var otpForm: some View {
        VStack(spacing: 16) {
            customField(placeholder: "6-digit verification code", text: $viewModel.otpCode)

            primaryButton(title: "Verify & Enter") {
                Task { await viewModel.verifyEmail() }
            }

            Button("Back to Login") {
                viewModel.needsOTP = false
                viewModel.errorMessage = nil
                viewModel.successMessage = nil
            }
            .font(.system(size: 14))
            .foregroundColor(UsColors.textMuted)
        }
    }

    private func customField(placeholder: String, text: Binding<String>, isSecure: Bool = false) -> some View {
        Group {
            if isSecure {
                SecureField(placeholder, text: text)
            } else {
                TextField(placeholder, text: text)
            }
        }
        .textFieldStyle(.plain)
        .font(.system(size: 15))
        .foregroundColor(UsColors.textPrimary)
        .padding(.horizontal, 16)
        .padding(.vertical, 14)
        .background(UsColors.bgSecondary)
        .clipShape(RoundedRectangle(cornerRadius: 12))
        .overlay(
            RoundedRectangle(cornerRadius: 12)
                .stroke(UsColors.borderMedium, lineWidth: 1)
        )
    }

    private func primaryButton(title: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            ZStack {
                Text(title)
                    .font(.system(size: 16, weight: .bold))
                    .foregroundColor(.black)
                    .opacity(viewModel.isLoading ? 0 : 1)

                if viewModel.isLoading {
                    ProgressView()
                        .tint(.black)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 14)
            .background(Color.white)
            .clipShape(Capsule())
        }
        .disabled(viewModel.isLoading)
    }
}
