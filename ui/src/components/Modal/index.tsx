import React, { useEffect, useRef } from 'react';
import { FiX } from 'react-icons/fi'; // Importing the close icon

interface ModalProps {
    isOpen: boolean;
    isLoading: boolean;
    onClose: () => void;
    title: string;
    children: React.ReactNode;
}

const Modal: React.FC<ModalProps> = ({ isOpen, onClose, title, children, isLoading }) => {
    const modalRef = useRef<HTMLDivElement>(null);

    // Close the modal when "Escape" key is pressed
    useEffect(() => {
        const handleKeyDown = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                onClose();
            }
        };

        if (isOpen) {
            window.addEventListener('keydown', handleKeyDown);
        } else {
            window.removeEventListener('keydown', handleKeyDown);
        }

        // Cleanup event listener when component unmounts or modal closes
        return () => {
            window.removeEventListener('keydown', handleKeyDown);
        };
    }, [isOpen, onClose]);

    // Close the modal when clicking outside of it
    useEffect(() => {
        const handleClickOutside = (event: MouseEvent) => {
            if (modalRef.current && !modalRef.current.contains(event.target as Node)) {
                onClose();
            }
        };

        if (isOpen) {
            window.addEventListener('mousedown', handleClickOutside);
        } else {
            window.removeEventListener('mousedown', handleClickOutside);
        }

        // Cleanup event listener when component unmounts or modal closes
        return () => {
            window.removeEventListener('mousedown', handleClickOutside);
        };
    }, [isOpen, onClose]);

    if (!isOpen) return null;

    return (
        <div className="fixed inset-0 bg-gray-900 bg-opacity-75 flex justify-center items-center z-50 p-4">
            <div
                ref={modalRef} // Reference to modal content to detect outside clicks
                className="bg-white rounded-lg p-6 w-2/3 max-h-[80vh] overflow-y-auto shadow-lg"
            >
                <div className="flex justify-between items-center mb-4">
                    <h2 className="text-xl font-semibold">{title}</h2>
                    <button
                        className="ml-4 p-2 text-gray-600 hover:text-gray-800"
                        onClick={onClose}
                    >
                        <FiX className="h-6 w-6" /> {/* Close icon */}
                    </button>
                </div>
                {/* if is loading render loading other wise children */}
                {isLoading ? (
                    <div className="flex items-center justify-center h-full min-h-[150px]">
                        <div className="spinner-border animate-spin inline-block w-8 h-8 border-4 rounded-full" role="status">
                            <span className="sr-only">Loading...</span>
                        </div>
                    </div>
                ) : (
                    children
                )}
            </div>
        </div>
    );
};

export default Modal;