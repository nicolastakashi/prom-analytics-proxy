import logo from '../../assets/logo.png'; // Adjusted relative path

function Header() {
    return (
        <header className="bg-blumine-900 text-white p-4 flex items-center">
            <img
                src={logo}
                alt="Logo"
                className="w-8 h-8 mr-2" // Smaller image size
            />
            <h1 className="text-xl">PromQL - Analytics Proxy</h1> {/* Smaller text size */}
        </header>
    );
}

export default Header;