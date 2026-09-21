// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import {ERC721} from "@openzeppelin/contracts/token/ERC721/ERC721.sol";
import {ERC721URIStorage} from "@openzeppelin/contracts/token/ERC721/extensions/ERC721URIStorage.sol";

/// @title ggstar Skill Badge
/// @notice Soulbound (non-transferable) ERC-721 badge representing an on-chain
///         GitHub skill reputation. One badge per wallet, forever non-transferable.
/// @dev Targets BOT Chain (EVM). Verified against OpenZeppelin Contracts v5.
contract ggstarSkillSBT is ERC721, ERC721URIStorage {
    struct BadgeData {
        string githubUsername;
        uint256 avatarId; // 1..100 (128x128 avatar)
        string customTitle; // e.g. "Solidity Princess"
        uint8 skillScore; // 1..100
        string[] skills; // user-curated final skills
        uint256 mintedAt;
    }

    /// @notice Token id that will be used for the next mint (starts at 1).
    uint256 public nextTokenId = 1;

    /// @notice Badge payload per token id.
    mapping(uint256 => BadgeData) public badgeDetails;

    /// @notice Owner of a token id (mint is 1 per wallet, so it is also indexable).
    mapping(uint256 => address) public badgeOwner;

    /// @notice One badge per wallet, permanently.
    mapping(address => bool) public hasMinted;

    /// @notice Token id owned by an address (0 = none).
    mapping(address => uint256) public tokenOf;

    event BadgeMinted(
        address indexed owner,
        uint256 indexed tokenId,
        string githubUsername,
        uint256 avatarId,
        uint8 skillScore
    );

    error AlreadyMinted(address owner);
    error InvalidAvatarId(uint256 avatarId);
    error InvalidSkillScore(uint8 skillScore);
    error EmptyUsername();
    error Soulbound();

    constructor() ERC721("ggstar Skill Badge", "KAWAII") {}

    /// @notice Mint a soulbound reputation badge. One per wallet.
    /// @param _githubUsername GitHub handle (non-empty, max 39 chars).
    /// @param _avatarId Avatar index, 1..100.
    /// @param _customTitle Free-form title shown on the badge.
    /// @param _skillScore AI-computed score, 1..100.
    /// @param _skills Final user-curated skills (empty entries are skipped off-chain).
    /// @param _tokenURI Metadata URI, generated server-side (data:application/json;base64).
    function mintBadge(
        string memory _githubUsername,
        uint256 _avatarId,
        string memory _customTitle,
        uint8 _skillScore,
        string[] memory _skills,
        string memory _tokenURI
    ) external returns (uint256) {
        if (hasMinted[msg.sender]) revert AlreadyMinted(msg.sender);
        if (_avatarId < 1 || _avatarId > 100) revert InvalidAvatarId(_avatarId);
        if (_skillScore < 1 || _skillScore > 100) revert InvalidSkillScore(_skillScore);
        if (bytes(_githubUsername).length == 0) revert EmptyUsername();

        uint256 tokenId = nextTokenId;
        nextTokenId = tokenId + 1;

        BadgeData storage badge = badgeDetails[tokenId];
        badge.githubUsername = _githubUsername;
        badge.avatarId = _avatarId;
        badge.customTitle = _customTitle;
        badge.skillScore = _skillScore;
        badge.mintedAt = block.timestamp;
        for (uint256 i = 0; i < _skills.length; i++) {
            badge.skills.push(_skills[i]);
        }

        hasMinted[msg.sender] = true;
        badgeOwner[tokenId] = msg.sender;
        tokenOf[msg.sender] = tokenId;

        _safeMint(msg.sender, tokenId);
        _setTokenURI(tokenId, _tokenURI);

        emit BadgeMinted(msg.sender, tokenId, _githubUsername, _avatarId, _skillScore);
        return tokenId;
    }

    /// @notice Read a wallet's badge. Reverts when the wallet holds no badge.
    function getBadgeByAddress(address _owner) external view returns (BadgeData memory) {
        uint256 tokenId = tokenOf[_owner];
        require(tokenId != 0, "SBT: No badge for this address");
        return badgeDetails[tokenId];
    }

    /// @notice Returns (hasBadge, tokenId) in a single round-trip for verification UIs.
    function badgeStatus(address _owner) external view returns (bool, uint256) {
        uint256 tokenId = tokenOf[_owner];
        return (tokenId != 0, tokenId);
    }

    function totalSupply() external view returns (uint256) {
        return nextTokenId - 1;
    }

    /// @dev Soulbound enforcement. Blocks every transfer except mint (from == 0)
    ///      and burn (to == 0). Used by transferFrom, safeTransferFrom and
    ///      safeBatchTransferFrom alike in OpenZeppelin v5.
    function _update(address to, uint256 tokenId, address auth)
        internal
        override
        returns (address)
    {
        address from = _ownerOf(tokenId);
        if (from != address(0) && to != address(0)) revert Soulbound();
        return super._update(to, tokenId, auth);
    }

    function tokenURI(uint256 tokenId)
        public
        view
        override(ERC721, ERC721URIStorage)
        returns (string memory)
    {
        return super.tokenURI(tokenId);
    }

    function supportsInterface(bytes4 interfaceId)
        public
        view
        override(ERC721, ERC721URIStorage)
        returns (bool)
    {
        return super.supportsInterface(interfaceId);
    }
}
