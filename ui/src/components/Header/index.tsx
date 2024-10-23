import logo from '../../assets/logo.png'; // Adjusted relative path

function Header() {
    return (
        <header className="bg-blumine-900 text-white p-4 flex items-center">
            <img
                src={logo}
                alt="Logo"
                className="w-8 h-8 mr-1" // Smaller image size
            />
            <h1 className="text-xl font-bold text-white">PromQL - Analytics Proxy</h1>
        </header>
    );
}

export default Header;